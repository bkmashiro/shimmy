package wasm

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newShimmyPythonShutdownTestDispatcher(t *testing.T, grace time.Duration, refill func(context.Context)) *ShimmyPythonDispatcher {
	t.Helper()
	dispatcher := NewShimmyPythonDispatcher(Config{}, nil)
	dispatcher.refillShutdownGrace = grace
	dispatcher.refillCtx, dispatcher.refillCancel = context.WithCancel(context.Background())
	dispatcher.refills.Add(1)
	go func() {
		defer dispatcher.refills.Done()
		refill(dispatcher.refillCtx)
	}()
	return dispatcher
}

func TestShimmyPythonShutdownAllowsHealthyRefillToFinishNaturally(t *testing.T) {
	result := make(chan string, 1)
	dispatcher := newShimmyPythonShutdownTestDispatcher(t, 50*time.Millisecond, func(ctx context.Context) {
		select {
		case <-time.After(5 * time.Millisecond):
			result <- "completed"
		case <-ctx.Done():
			result <- "canceled"
		}
	})

	require.NoError(t, dispatcher.Shutdown(context.Background()))
	require.Equal(t, "completed", <-result)
}

func TestShimmyPythonShutdownCancelsRefillOnlyAfterGrace(t *testing.T) {
	const grace = 20 * time.Millisecond
	canceled := make(chan struct{})
	dispatcher := newShimmyPythonShutdownTestDispatcher(t, grace, func(ctx context.Context) {
		<-ctx.Done()
		close(canceled)
	})

	started := time.Now()
	require.NoError(t, dispatcher.Shutdown(context.Background()))
	elapsed := time.Since(started)
	require.GreaterOrEqual(t, elapsed, grace)
	require.Less(t, elapsed, 250*time.Millisecond)
	select {
	case <-canceled:
	default:
		t.Fatal("refill context was not canceled after the grace period")
	}
}

func TestShimmyPythonShutdownHonorsDeadlineWhileCleanupContinues(t *testing.T) {
	releaseCleanup := make(chan struct{})
	dispatcher := newShimmyPythonShutdownTestDispatcher(t, 5*time.Millisecond, func(ctx context.Context) {
		<-ctx.Done()
		<-releaseCleanup
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := dispatcher.Shutdown(ctx)
	elapsed := time.Since(started)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, elapsed, 100*time.Millisecond)

	close(releaseCleanup)
	require.NoError(t, dispatcher.Shutdown(context.Background()), "a later shutdown call must wait for background cleanup")
}

func TestShimmyPythonClosedDispatcherRejectsRefillPublication(t *testing.T) {
	dispatcher := NewShimmyPythonDispatcher(Config{}, nil)
	dispatcher.prepared = make(chan *shimmyPythonModuleSlot, 1)
	dispatcher.mu.Lock()
	dispatcher.closed = true
	dispatcher.mu.Unlock()

	require.False(t, dispatcher.publishSingleUseRefill(&shimmyPythonModuleSlot{id: 1}))
	require.Empty(t, dispatcher.prepared)
}

func TestShimmyPythonExactArtifactShutdownDrainsHealthyRefills(t *testing.T) {
	wasmPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_WASM")
	manifestPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_MANIFEST")
	expectedCommit := os.Getenv("SHIMMY_PYTHON_EXPECTED_COMMIT")
	if wasmPath == "" || manifestPath == "" || expectedCommit == "" {
		t.Skip("SHIMMY_PYTHON_RUNTIME_WASM, SHIMMY_PYTHON_RUNTIME_MANIFEST, and SHIMMY_PYTHON_EXPECTED_COMMIT are required")
	}

	scriptPath := filepath.Join(t.TempDir(), "shutdown_eval.py")
	require.NoError(t, os.WriteFile(scriptPath, []byte(`def evaluation_function(response, answer, params=None):
    return {"is_correct": response == answer}
`), 0o600))

	var eventsMu sync.Mutex
	var events []ShimmyPythonPhaseEvent
	dispatcher := NewShimmyPythonDispatcher(Config{
		ModulePath:                 wasmPath,
		ShimmyPythonManifestPath:   manifestPath,
		ShimmyPythonExpectedCommit: expectedCommit,
		PythonScriptPath:           scriptPath,
		PythonLifecycle:            "single-use",
		MaxInstances:               3,
		PythonPreparedCapacity:     3,
		MaxMemoryPages:             8192,
		Timeout:                    2 * time.Minute,
		ShimmyPythonObserver: func(event ShimmyPythonPhaseEvent) {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
		},
	}, zap.NewNop())
	require.NoError(t, dispatcher.Start(context.Background()))

	var requests sync.WaitGroup
	requestErrors := make(chan error, 3)
	requests.Add(3)
	for range 3 {
		go func() {
			defer requests.Done()
			_, err := dispatcher.Send(context.Background(), "eval", map[string]any{
				"response": "42",
				"answer":   "42",
				"params":   map[string]any{},
			})
			requestErrors <- err
		}()
	}
	requests.Wait()
	close(requestErrors)
	for err := range requestErrors {
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		dispatcher.refillMu.Lock()
		defer dispatcher.refillMu.Unlock()
		return dispatcher.refillInFlight == 3
	}, time.Second, 5*time.Millisecond, "all three consumed slots must have an in-flight refill")

	started := time.Now()
	require.NoError(t, dispatcher.Shutdown(context.Background()))
	shutdownDuration := time.Since(started)
	require.Less(t, shutdownDuration, 5*time.Second)
	t.Logf("shutdown_duration=%s", shutdownDuration)

	dispatcher.refillMu.Lock()
	require.Zero(t, dispatcher.refillInFlight)
	dispatcher.refillMu.Unlock()
	dispatcher.mu.Lock()
	require.Nil(t, dispatcher.runtime)
	require.Nil(t, dispatcher.compiled)
	require.Nil(t, dispatcher.prepared)
	require.Nil(t, dispatcher.refillCtx)
	require.Nil(t, dispatcher.refillCancel)
	dispatcher.mu.Unlock()
	require.Zero(t, dispatcher.preparedRefills.Load(), "closed dispatcher must reject completed refill slots")

	eventsMu.Lock()
	defer eventsMu.Unlock()
	guestInitOK := 0
	for _, event := range events {
		if event.Purpose != ShimmyPythonPurposeRefill {
			continue
		}
		require.NotEqual(t, ShimmyPythonOutcomeError, event.Outcome, "%+v", event)
		if event.Phase == ShimmyPythonPhaseGuestInit && event.Outcome == ShimmyPythonOutcomeOK {
			guestInitOK++
		}
	}
	require.Equal(t, 3, guestInitOK)
	t.Logf("refill_guest_init_ok=%d prepared_refills=%d", guestInitOK, dispatcher.preparedRefills.Load())
}

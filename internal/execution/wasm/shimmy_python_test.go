package wasm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"go.uber.org/zap"
)

type shimmyPythonSnapshotSelectionTestStrategy struct {
	SnapshotStrategy
	selected string
}

func (s *shimmyPythonSnapshotSelectionTestStrategy) selectedSnapshotMode() string {
	return s.selected
}

func TestShimmyPythonRejectsHostFilesystemPaths(t *testing.T) {
	t.Setenv("FUNCTION_WASM_ALLOWED_PATHS", "/tmp")
	dispatcher := NewShimmyPythonDispatcher(Config{}, zap.NewNop())
	err := dispatcher.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not expose Host filesystem paths")
}

func TestShimmyPythonDispatcherRealNumPyArtifactCompatibility(t *testing.T) {
	wasmPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_WASM")
	manifestPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_MANIFEST")
	expectedCommit := os.Getenv("SHIMMY_PYTHON_EXPECTED_COMMIT")
	if wasmPath == "" || manifestPath == "" || expectedCommit == "" {
		t.Skip("SHIMMY_PYTHON_RUNTIME_WASM, SHIMMY_PYTHON_RUNTIME_MANIFEST, and SHIMMY_PYTHON_EXPECTED_COMMIT are required")
	}

	scriptPath := filepath.Join(t.TempDir(), "eval.py")
	script := `
import numpy as np
_counter = 0

def evaluation_function(response, answer, params=None):
    global _counter
    _counter += 1
    if response == "explode":
        raise ValueError("expected explosion")

    if response == "float128":
        one = np.longdouble("1")
        wide = np.longdouble("1.0000000000000000000000000000000002")
        return {
            "longdouble_itemsize": int(np.dtype(np.longdouble).itemsize),
            "longdouble_nmant": int(np.finfo(np.longdouble).nmant),
            "double_nmant": int(np.finfo(np.double).nmant),
            "preserves_extra_precision": bool(wide > one),
            "narrows_to_double_one": bool(float(wide) == 1.0),
            "epsilon_is_narrower": bool(np.finfo(np.longdouble).eps < np.finfo(np.double).eps),
            "counter": _counter,
        }
    return {"is_correct": response == answer, "counter": _counter}

def preview_function(response, answer, params=None):
    return {"preview": f"response={response}"}
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o644))

	dispatcher := NewShimmyPythonDispatcher(Config{
		ModulePath:                 wasmPath,
		ShimmyPythonManifestPath:   manifestPath,
		ShimmyPythonExpectedCommit: expectedCommit,
		PythonScriptPath:           scriptPath,
		PythonLifecycle:            "snapshot",
		SnapshotMode:               "memcpy",
		MaxInstances:               1,
		MaxMemoryPages:             8192,
		Timeout:                    120 * time.Second,
	}, zap.NewNop())
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer startCancel()
	require.NoError(t, dispatcher.Start(startContext))
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dispatcher.Shutdown(shutdownContext)
	})

	health, err := dispatcher.Send(context.Background(), "healthcheck", nil)
	require.NoError(t, err)
	assert.Equal(t, "snapshot", health["result"].(map[string]any)["lifecycle"])
	assert.Equal(t, "memcpy", health["result"].(map[string]any)["snapshot_mode"])
	assert.Equal(t, "linear-memory-memcpy", health["result"].(map[string]any)["reset_mode"])

	first, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, true, first["result"].(map[string]any)["is_correct"])
	assert.Equal(t, float64(1), first["result"].(map[string]any)["counter"])

	second, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), second["result"].(map[string]any)["counter"], "snapshot restore must not retain globals")

	preview, err := dispatcher.Send(context.Background(), "preview", map[string]any{"response": "3.14", "answer": "3.14"})
	require.NoError(t, err)
	assert.Equal(t, "response=3.14", preview["result"].(map[string]any)["preview"])

	failure, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "explode", "answer": "x"})
	require.NoError(t, err)
	assert.Equal(t, "ValueError", failure["result"].(map[string]any)["error_type"])

	longdouble, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "float128", "answer": "x"})
	require.NoError(t, err)
	value := longdouble["result"].(map[string]any)
	assert.Equal(t, float64(16), value["longdouble_itemsize"])
	assert.GreaterOrEqual(t, value["longdouble_nmant"].(float64), float64(112))
	assert.Greater(t, value["longdouble_nmant"].(float64), value["double_nmant"].(float64))
	assert.Equal(t, false, value["preserves_extra_precision"], "WASI longdouble string parsing is binary64 fallback")
	assert.Equal(t, true, value["narrows_to_double_one"])
	assert.Equal(t, true, value["epsilon_is_narrower"])
}

func TestShimmyPythonSnapshotStrategyName(t *testing.T) {
	assert.Equal(t, "memcpy", shimmyPythonSnapshotStrategyName(NewFullMemcpyStrategy()))
	assert.Equal(t, "cow", shimmyPythonSnapshotStrategyName(&shimmyPythonSnapshotSelectionTestStrategy{
		SnapshotStrategy: NewFullMemcpyStrategy(),
		selected:         "cow",
	}))
	assert.Equal(t, "memcpy", shimmyPythonSnapshotStrategyName(&shimmyPythonSnapshotSelectionTestStrategy{
		SnapshotStrategy: NewFullMemcpyStrategy(),
		selected:         "memcpy",
	}))
}

func TestAcquireShimmyPythonSnapshotSlotReplenishesMissingSlot(t *testing.T) {
	prepared := make(chan *shimmyPythonModuleSlot, 1)
	closed := make(chan struct{})
	want := &shimmyPythonModuleSlot{snapshotSelected: "memcpy"}
	calls := 0

	got, err := acquireShimmyPythonSnapshotSlot(
		context.Background(),
		prepared,
		closed,
		func(context.Context) (*shimmyPythonModuleSlot, error) {
			calls++
			return want, nil
		},
	)

	require.NoError(t, err)
	assert.Same(t, want, got)
	assert.Equal(t, 1, calls)
}

func TestAcquireShimmyPythonSnapshotSlotReturnsReplenishFailure(t *testing.T) {
	prepared := make(chan *shimmyPythonModuleSlot, 1)
	closed := make(chan struct{})
	wantErr := errors.New("replacement unavailable")

	_, err := acquireShimmyPythonSnapshotSlot(
		context.Background(),
		prepared,
		closed,
		func(context.Context) (*shimmyPythonModuleSlot, error) {
			return nil, wantErr
		},
	)

	require.ErrorIs(t, err, wantErr)
}

func TestRestoreShimmyPythonSnapshotRejectsMemoryGrowth(t *testing.T) {
	ctx := context.Background()
	rt, compiled := compileEchoModule(t, ctx, echoWasmBytes(t))
	t.Cleanup(func() { require.NoError(t, rt.Close(ctx)) })
	module, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	require.NoError(t, err)

	strategy := NewFullMemcpyStrategy()
	require.NoError(t, strategy.Take(module.Memory()))
	slot := &shimmyPythonModuleSlot{
		module:       module,
		strategy:     strategy,
		baselineSize: module.Memory().Size(),
	}
	_, grew := module.Memory().Grow(1)
	require.True(t, grew)

	err = restoreShimmyPythonSnapshot(slot)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory size drift")
}

func TestShimmyPythonDispatcherRealNumPyCOWRestoresState(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("COW memory allocator is Linux-only")
	}
	wasmPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_WASM")
	manifestPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_MANIFEST")
	expectedCommit := os.Getenv("SHIMMY_PYTHON_EXPECTED_COMMIT")
	if wasmPath == "" || manifestPath == "" || expectedCommit == "" {
		t.Skip("SHIMMY_PYTHON_RUNTIME_WASM, SHIMMY_PYTHON_RUNTIME_MANIFEST, and SHIMMY_PYTHON_EXPECTED_COMMIT are required")
	}

	scriptPath := filepath.Join(t.TempDir(), "cow.py")
	script := `
_counter = 0

def evaluation_function(response, answer, params=None):
    global _counter
    _counter += 1
    return {"counter": _counter, "is_correct": response == answer}
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o644))

	dispatcher := NewShimmyPythonDispatcher(Config{
		ModulePath:                 wasmPath,
		ShimmyPythonManifestPath:   manifestPath,
		ShimmyPythonExpectedCommit: expectedCommit,
		PythonScriptPath:           scriptPath,
		PythonLifecycle:            "snapshot",
		SnapshotMode:               "cow",
		MaxInstances:               2,
		MaxMemoryPages:             8192,
		Timeout:                    120 * time.Second,
	}, zap.NewNop())
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer startCancel()
	require.NoError(t, dispatcher.Start(startContext))
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dispatcher.Shutdown(shutdownContext)
	})

	health, err := dispatcher.Send(context.Background(), "healthcheck", nil)
	require.NoError(t, err)
	assert.Equal(t, "cow", health["result"].(map[string]any)["snapshot_selected"])
	assert.Equal(t, "linear-memory-cow", health["result"].(map[string]any)["reset_mode"])
	assert.Equal(t, 2, health["result"].(map[string]any)["prepared_ready"])

	for i := 0; i < 2; i++ {
		result, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
		require.NoError(t, err)
		assert.Equal(t, float64(1), result["result"].(map[string]any)["counter"])
	}
}

func TestShimmyPythonDispatcherCowTimeoutDiscardsInvalidatedSlot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("prepared-memory COW requires Linux")
	}
	wasmPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_WASM")
	manifestPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_MANIFEST")
	expectedCommit := os.Getenv("SHIMMY_PYTHON_EXPECTED_COMMIT")
	if wasmPath == "" || manifestPath == "" || expectedCommit == "" {
		t.Skip("SHIMMY_PYTHON_RUNTIME_WASM, SHIMMY_PYTHON_RUNTIME_MANIFEST, and SHIMMY_PYTHON_EXPECTED_COMMIT are required")
	}

	scriptPath := filepath.Join(t.TempDir(), "cow-timeout.py")
	script := `
import sys

def evaluation_function(response, answer, params=None):
    if response == "loop":
        print("cow-timeout-loop-entered", file=sys.stderr, flush=True)
        while True:
            pass
    return {"is_correct": response == answer}
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o644))

	var eventsMu sync.Mutex
	var events []ShimmyPythonPhaseEvent
	dispatcher := NewShimmyPythonDispatcher(Config{
		ModulePath:                  wasmPath,
		ShimmyPythonManifestPath:    manifestPath,
		PythonScriptPath:            scriptPath,
		PythonLifecycle:             "snapshot",
		SnapshotMode:                "cow",
		MaxInstances:                1,
		PythonSnapshotHeadroomBytes: 32 << 20,
		MaxMemoryPages:              16384,
		Timeout:                     2 * time.Minute,
		ShimmyPythonObserver: func(event ShimmyPythonPhaseEvent) {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
		},
	}, zap.NewNop())
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer startCancel()
	require.NoError(t, dispatcher.Start(startContext))
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dispatcher.Shutdown(shutdownContext)
	})

	health, err := dispatcher.Send(context.Background(), "healthcheck", nil)
	require.NoError(t, err)
	assert.Equal(t, "cow", health["result"].(map[string]any)["snapshot_selected"])
	eventsMu.Lock()
	events = nil
	eventsMu.Unlock()

	timeoutContext, timeoutCancel := context.WithTimeout(context.Background(), time.Second)
	_, err = dispatcher.Send(timeoutContext, "eval", map[string]any{"response": "loop", "answer": "x"})
	timeoutCancel()
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "cow-timeout-loop-entered", "the guest must enter the target execution segment before cancellation")
	eventsMu.Lock()
	timeoutEvents := append([]ShimmyPythonPhaseEvent(nil), events...)
	events = nil
	eventsMu.Unlock()
	for _, event := range timeoutEvents {
		if event.Purpose == ShimmyPythonPurposeRequest {
			require.NotEqual(t, ShimmyPythonPhaseRestore, event.Phase, "%+v", event)
		}
	}

	after, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, true, after["result"].(map[string]any)["is_correct"])
	eventsMu.Lock()
	recoveryEvents := append([]ShimmyPythonPhaseEvent(nil), events...)
	eventsMu.Unlock()
	restoreObserved := false
	for _, event := range recoveryEvents {
		if event.Purpose == ShimmyPythonPurposeRequest && event.Phase == ShimmyPythonPhaseRestore {
			require.Equal(t, ShimmyPythonOutcomeOK, event.Outcome)
			restoreObserved = true
		}
	}
	require.True(t, restoreObserved, "the successful recovery request must restore its COW baseline")

	health, err = dispatcher.Send(context.Background(), "healthcheck", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, health["result"].(map[string]any)["prepared_ready"])
}

func TestShimmyPythonDispatcherSingleUsePreparedRefillsNeverServedCandidates(t *testing.T) {
	wasmPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_WASM")
	manifestPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_MANIFEST")
	expectedCommit := os.Getenv("SHIMMY_PYTHON_EXPECTED_COMMIT")
	if wasmPath == "" || manifestPath == "" || expectedCommit == "" {
		t.Skip("SHIMMY_PYTHON_RUNTIME_WASM, SHIMMY_PYTHON_RUNTIME_MANIFEST, and SHIMMY_PYTHON_EXPECTED_COMMIT are required")
	}

	scriptPath := filepath.Join(t.TempDir(), "single-use.py")
	script := `
_counter = 0

def evaluation_function(response, answer, params=None):
    global _counter
    _counter += 1
    return {"counter": _counter, "is_correct": response == answer}
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o644))

	dispatcher := NewShimmyPythonDispatcher(Config{
		ModulePath:                 wasmPath,
		ShimmyPythonManifestPath:   manifestPath,
		ShimmyPythonExpectedCommit: expectedCommit,
		PythonScriptPath:           scriptPath,
		PythonLifecycle:            "single-use",
		PythonPreparedCapacity:     1,
		MaxInstances:               1,
		MaxMemoryPages:             8192,
		Timeout:                    120 * time.Second,
	}, zap.NewNop())
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer startCancel()
	require.NoError(t, dispatcher.Start(startContext))
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dispatcher.Shutdown(shutdownContext)
	})

	health, err := dispatcher.Send(context.Background(), "healthcheck", nil)
	require.NoError(t, err)
	assert.Equal(t, "single-use", health["result"].(map[string]any)["lifecycle"])
	assert.Equal(t, 1, health["result"].(map[string]any)["prepared_ready"])
	assert.Equal(t, "single-use-prepared", health["result"].(map[string]any)["reset_mode"])

	first, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), first["result"].(map[string]any)["counter"])

	// The hit starts a slow background refill. An immediate next request must not
	// wait for it; it initializes one fresh single-use fallback synchronously.
	second, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), second["result"].(map[string]any)["counter"])

	require.Eventually(t, func() bool {
		health, err = dispatcher.Send(context.Background(), "healthcheck", nil)
		if err != nil {
			return false
		}
		state := health["result"].(map[string]any)
		return state["prepared_ready"] == 1 && state["prepared_refills"] == uint64(1)
	}, 2*time.Minute, 100*time.Millisecond)

	health, err = dispatcher.Send(context.Background(), "healthcheck", nil)
	require.NoError(t, err)
	state := health["result"].(map[string]any)
	assert.Equal(t, uint64(1), state["prepared_hits"])
	assert.Equal(t, uint64(1), state["prepared_misses"])

	third, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), third["result"].(map[string]any)["counter"])

	health, err = dispatcher.Send(context.Background(), "healthcheck", nil)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), health["result"].(map[string]any)["prepared_hits"])
}

func TestShimmyPythonDispatcherTimeoutDoesNotPoisonRuntime(t *testing.T) {
	wasmPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_WASM")
	manifestPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_MANIFEST")
	expectedCommit := os.Getenv("SHIMMY_PYTHON_EXPECTED_COMMIT")
	if wasmPath == "" || manifestPath == "" || expectedCommit == "" {
		t.Skip("SHIMMY_PYTHON_RUNTIME_WASM, SHIMMY_PYTHON_RUNTIME_MANIFEST, and SHIMMY_PYTHON_EXPECTED_COMMIT are required")
	}

	scriptPath := filepath.Join(t.TempDir(), "timeout.py")
	script := `
def evaluation_function(response, answer, params=None):
    if response == "loop":
        while True:
            pass
    return {"is_correct": response == answer}
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o644))

	dispatcher := NewShimmyPythonDispatcher(Config{
		ModulePath:                 wasmPath,
		ShimmyPythonManifestPath:   manifestPath,
		ShimmyPythonExpectedCommit: expectedCommit,
		PythonScriptPath:           scriptPath,
		MaxInstances:               1,
		MaxMemoryPages:             8192,
		Timeout:                    12 * time.Second,
	}, zap.NewNop())
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer startCancel()
	require.NoError(t, dispatcher.Start(startContext))
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dispatcher.Shutdown(shutdownContext)
	})

	_, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "loop", "answer": "x"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	after, err := dispatcher.Send(context.Background(), "eval", map[string]any{"response": "42", "answer": "42"})
	require.NoError(t, err)
	assert.Equal(t, true, after["result"].(map[string]any)["is_correct"])
}

func TestShimmyPythonDispatcherRealLambdaFeedbackBundle(t *testing.T) {
	wasmPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_WASM")
	manifestPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_MANIFEST")
	expectedCommit := os.Getenv("SHIMMY_PYTHON_EXPECTED_COMMIT")
	if wasmPath == "" || manifestPath == "" || expectedCommit == "" {
		t.Skip("set SHIMMY_PYTHON_RUNTIME_WASM, SHIMMY_PYTHON_RUNTIME_MANIFEST, and SHIMMY_PYTHON_EXPECTED_COMMIT")
	}

	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
	bundlePath := filepath.Join(t.TempDir(), "boilerplate.bundle.py")
	command := exec.Command("python3",
		filepath.Join(repoRoot, "tools", "lf-bundle-python", "lf_bundle_python.py"),
		"--root", filepath.Join(repoRoot, "examples", "lambda-feedback-fixtures", "boilerplate-python"),
		"--adapter-root", filepath.Join(repoRoot, "examples", "lambda-feedback-adapter"),
		"--eval-entrypoint", "evaluation_function.evaluation:evaluation_function",
		"--preview-entrypoint", "evaluation_function.preview:preview_function",
		"--out", bundlePath,
	)
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))

	dispatcher := NewShimmyPythonDispatcher(Config{
		ModulePath:                 wasmPath,
		ShimmyPythonManifestPath:   manifestPath,
		ShimmyPythonExpectedCommit: expectedCommit,
		PythonScriptPath:           bundlePath,
		MaxMemoryPages:             8192,
		MaxInstances:               1,
		Timeout:                    2 * time.Minute,
	}, zap.NewNop())
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer startCancel()
	require.NoError(t, dispatcher.Start(startContext))
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dispatcher.Shutdown(shutdownContext)
	})

	evalResult, err := dispatcher.Send(context.Background(), "eval", map[string]any{
		"response": "same",
		"answer":   "same",
		"params":   map[string]any{},
	})
	require.NoError(t, err)
	assert.Equal(t, true, evalResult["result"].(map[string]any)["is_correct"])

	previewResult, err := dispatcher.Send(context.Background(), "preview", map[string]any{
		"response": "x+y",
		"params":   map[string]any{},
	})
	require.NoError(t, err)
	preview := previewResult["result"].(map[string]any)["preview"].(map[string]any)
	assert.Equal(t, "x+y", preview["sympy"])
}

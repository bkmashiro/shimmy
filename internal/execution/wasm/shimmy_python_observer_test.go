package wasm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestShimmyPythonObserverRecordsPhaseIdentity(t *testing.T) {
	var got ShimmyPythonPhaseEvent
	dispatcher := NewShimmyPythonDispatcher(Config{
		ShimmyPythonObserver: func(event ShimmyPythonPhaseEvent) {
			got = event
		},
	}, zap.NewNop())
	started := time.Now().Add(-time.Millisecond)

	dispatcher.observeShimmyPythonPhase(ShimmyPythonPhaseObservation{
		Phase:     ShimmyPythonPhaseEvaluate,
		Purpose:   ShimmyPythonPurposeRequest,
		RequestID: 7,
		SlotID:    11,
		Started:   started,
		Outcome:   ShimmyPythonOutcomeOK,
	})

	assert.Equal(t, ShimmyPythonPhaseEvaluate, got.Phase)
	assert.Equal(t, ShimmyPythonPurposeRequest, got.Purpose)
	assert.Equal(t, uint64(7), got.RequestID)
	assert.Equal(t, uint64(11), got.SlotID)
	assert.Equal(t, ShimmyPythonOutcomeOK, got.Outcome)
	assert.GreaterOrEqual(t, got.Duration, time.Millisecond)
}

func TestShimmyPythonObserverNilIsNoop(t *testing.T) {
	dispatcher := NewShimmyPythonDispatcher(Config{}, zap.NewNop())
	require.NotPanics(t, func() {
		dispatcher.observeShimmyPythonPhase(ShimmyPythonPhaseObservation{
			Phase:   ShimmyPythonPhaseCompile,
			Started: time.Now(),
			Outcome: ShimmyPythonOutcomeOK,
		})
	})
}

func TestShimmyPythonObserverPanicDoesNotAffectRuntime(t *testing.T) {
	dispatcher := NewShimmyPythonDispatcher(Config{
		ShimmyPythonObserver: func(ShimmyPythonPhaseEvent) {
			panic("observer failed")
		},
	}, zap.NewNop())

	require.NotPanics(t, func() {
		dispatcher.observeShimmyPythonPhase(ShimmyPythonPhaseObservation{
			Phase:   ShimmyPythonPhaseCompile,
			Started: time.Now(),
			Outcome: ShimmyPythonOutcomeError,
			Err:     assert.AnError,
		})
	})
}

func TestShimmyPythonStartupObserverMayShutdownDispatcher(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "eval.py")
	require.NoError(t, os.WriteFile(scriptPath, []byte("def evaluation_function(response, answer, params=None):\n    return {}\n"), 0o600))

	var dispatcher *ShimmyPythonDispatcher
	dispatcher = NewShimmyPythonDispatcher(Config{
		ModulePath:               filepath.Join(t.TempDir(), "missing.wasm"),
		ShimmyPythonManifestPath: filepath.Join(t.TempDir(), "missing-manifest.json"),
		PythonScriptPath:         scriptPath,
		ShimmyPythonObserver: func(ShimmyPythonPhaseEvent) {
			_ = dispatcher.Shutdown(context.Background())
		},
	}, zap.NewNop())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Start(context.Background()) }()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("startup observer deadlocked while calling Shutdown")
	}
}

package wasm

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestShimmyPythonObserverExactArtifactPhaseOrder(t *testing.T) {
	wasmPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_WASM")
	manifestPath := os.Getenv("SHIMMY_PYTHON_RUNTIME_MANIFEST")
	if wasmPath == "" || manifestPath == "" {
		t.Skip("SHIMMY_PYTHON_RUNTIME_WASM and SHIMMY_PYTHON_RUNTIME_MANIFEST are required")
	}

	scriptPath := filepath.Join(t.TempDir(), "observer_eval.py")
	require.NoError(t, os.WriteFile(scriptPath, []byte(`_counter = 0
def evaluation_function(response, answer, params=None):
    global _counter
    _counter += 1
    return {"is_correct": response == answer, "guest_invocation_count": _counter}
`), 0o600))

	var mu sync.Mutex
	var events []ShimmyPythonPhaseEvent
	dispatcher := NewShimmyPythonDispatcher(Config{
		ModulePath:               wasmPath,
		ShimmyPythonManifestPath: manifestPath,
		PythonScriptPath:         scriptPath,
		PythonLifecycle:          "snapshot",
		SnapshotMode:             "memcpy",
		MaxInstances:             1,
		MaxMemoryPages:           8192,
		Timeout:                  2 * time.Minute,
		ShimmyPythonObserver: func(event ShimmyPythonPhaseEvent) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		},
	}, zap.NewNop())
	require.NoError(t, dispatcher.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, dispatcher.Shutdown(context.Background())) })

	response, err := dispatcher.Send(context.Background(), "eval", map[string]any{
		"response": "42",
		"answer":   "42",
		"params":   map[string]any{},
	})
	require.NoError(t, err)
	assert.Equal(t, float64(1), response["result"].(map[string]any)["guest_invocation_count"])

	mu.Lock()
	got := append([]ShimmyPythonPhaseEvent(nil), events...)
	mu.Unlock()

	assertObservedPhaseSubsequence(t, got, ShimmyPythonPurposeStartup, []ShimmyPythonPhase{
		ShimmyPythonPhaseArtifactVerify,
		ShimmyPythonPhaseRuntimeCreate,
		ShimmyPythonPhaseWASIImports,
		ShimmyPythonPhaseCompile,
		ShimmyPythonPhaseInstantiate,
		ShimmyPythonPhaseInitialize,
		ShimmyPythonPhaseRuntimeInit,
		ShimmyPythonPhaseRuntimePrepare,
		ShimmyPythonPhaseHeadroom,
		ShimmyPythonPhaseSnapshotTake,
	})
	assertObservedPhaseSubsequence(t, got, ShimmyPythonPurposeRequest, []ShimmyPythonPhase{
		ShimmyPythonPhaseCheckout,
		ShimmyPythonPhaseEvaluate,
		ShimmyPythonPhaseRestore,
		ShimmyPythonPhaseDecode,
	})
	for _, event := range got {
		assert.NotEqual(t, ShimmyPythonOutcomeError, event.Outcome, "%+v", event)
		assert.GreaterOrEqual(t, event.Duration, time.Duration(0))
	}
}

func assertObservedPhaseSubsequence(t *testing.T, events []ShimmyPythonPhaseEvent, purpose ShimmyPythonPurpose, want []ShimmyPythonPhase) {
	t.Helper()
	index := 0
	for _, event := range events {
		if event.Purpose != purpose || index == len(want) {
			continue
		}
		if event.Phase == want[index] {
			index++
		}
	}
	if index != len(want) {
		t.Fatalf("observed %d/%d phases for purpose %q; events=%+v", index, len(want), purpose, events)
	}
}

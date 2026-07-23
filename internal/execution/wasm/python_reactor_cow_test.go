//go:build linux

package wasm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCowPythonReactorPreparedBaselines(t *testing.T) {
	wasmPath := os.Getenv("PYTHON_REACTOR_WASM")
	if wasmPath == "" {
		t.Skip("PYTHON_REACTOR_WASM not set")
	}

	evaluatorPath := filepath.Join(t.TempDir(), "cow_evaluator.py")
	evaluator := []byte(`
_counter = 0

def evaluation_function(response, answer, params=None):
    global _counter
    if params and params.get("loop"):
        while True:
            pass
    _counter += 1
    return {"is_correct": True, "feedback": f"counter={_counter}"}
`)
	require.NoError(t, os.WriteFile(evaluatorPath, evaluator, 0o600))

	cfg := Config{
		ModulePath:                  wasmPath,
		PythonScriptPath:            evaluatorPath,
		PythonPreloadMode:           "evaluator",
		PythonSnapshotHeadroomBytes: 8 * 1024 * 1024,
		SnapshotMode:                "cow",
		MaxInstances:                2,
		MaxMemoryPages:              8192,
		CompileCacheDir:             filepath.Join(t.TempDir(), "wazero-cache"),
		Timeout:                     120 * time.Second,
	}
	dispatcher := NewReactorPythonDispatcher(cfg, newTestLogger(t))
	initCtx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	require.NoError(t, dispatcher.Start(initCtx))
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		require.NoError(t, dispatcher.Shutdown(shutdownCtx))
	})

	require.NotNil(t, dispatcher.cowCoordinator)
	imageID := dispatcher.cowCoordinator.ImageID()
	require.NotEmpty(t, imageID)
	memorySize := assertCowPythonPoolAtBaseline(t, dispatcher, imageID)

	for i := range 6 {
		result, err := dispatcher.Send(context.Background(), "eval", map[string]any{
			"response": "x",
			"answer":   "x",
			"params":   map[string]any{"iteration": i},
		})
		require.NoError(t, err, "request %d", i)
		payload, ok := result["result"].(map[string]any)
		require.True(t, ok, "request %d result payload: %#v", i, result["result"])
		feedback, _ := payload["feedback"].(string)
		require.True(t, strings.Contains(feedback, "counter=1"),
			"request %d must begin from prepared baseline, got %q", i, feedback)

		// SendRequest now restores before it returns; pooled idle runners must not
		// retain request-private COW pages until their next lease.
		memorySize = assertCowPythonPoolAtBaseline(t, dispatcher, imageID)
	}

	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, timeoutErr := dispatcher.Send(timeoutCtx, "eval", map[string]any{
		"response": "x",
		"answer":   "x",
		"params":   map[string]any{"loop": true},
	})
	timeoutCancel()
	require.Error(t, timeoutErr, "infinite evaluator must be interrupted and discarded")
	require.Eventually(t, func() bool {
		return len(dispatcher.pool) == cap(dispatcher.pool)
	}, 5*time.Minute, 250*time.Millisecond, "timed-out runner must be replaced")
	memorySize = assertCowPythonPoolAtBaseline(t, dispatcher, imageID)

	writeCowPythonEvidence(t, dispatcher, imageID, memorySize)
}

func assertCowPythonPoolAtBaseline(t *testing.T, dispatcher *ReactorPythonDispatcher, imageID string) uint32 {
	t.Helper()
	runners := make([]*ReactorPythonRunner, 0, cap(dispatcher.pool))
	var memorySize uint32
	for range cap(dispatcher.pool) {
		runner := <-dispatcher.pool
		runners = append(runners, runner)

		strategy, ok := runner.strategy.(*CowSnapshotStrategy)
		require.True(t, ok, "expected CowSnapshotStrategy, got %T", runner.strategy)
		require.True(t, strategy.UsingCow(), "prepared Python runner fell back from COW")
		require.Equal(t, imageID, strategy.ImageID())

		memory := runner.mod.Memory()
		if memorySize == 0 {
			memorySize = memory.Size()
		}
		require.Equal(t, memorySize, memory.Size(), "all attached prepared images must have the same size")
		buf, ok := memory.Read(0, memory.Size())
		require.True(t, ok)
		digest := sha256.Sum256(buf)
		require.Equal(t, imageID, hex.EncodeToString(digest[:]),
			"idle runner must already be reset to the canonical prepared image")
	}
	for _, runner := range runners {
		dispatcher.pool <- runner
	}
	return memorySize
}

func writeCowPythonEvidence(t *testing.T, dispatcher *ReactorPythonDispatcher, imageID string, memorySize uint32) {
	t.Helper()
	path := os.Getenv("COW_EVIDENCE_PATH")
	if path == "" {
		return
	}
	artifactDigest := sha256.Sum256(dispatcher.wasmBytes)
	kernelBytes, _ := os.ReadFile("/proc/version")
	evidence := map[string]any{
		"artifact_sha256":        hex.EncodeToString(artifactDigest[:]),
		"canonical_image_sha256": imageID,
		"memory_bytes":           memorySize,
		"instances":              cap(dispatcher.pool),
		"successful_requests":    6,
		"timeout_replacements":   1,
		"fallback_count":         0,
		"go_version":             runtime.Version(),
		"platform":               runtime.GOOS + "/" + runtime.GOARCH,
		"kernel":                 strings.TrimSpace(string(kernelBytes)),
		"wazero_version":         "v1.11.0",
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(encoded, '\n'), 0o644))
	t.Logf("COW evidence: %s", encoded)
}

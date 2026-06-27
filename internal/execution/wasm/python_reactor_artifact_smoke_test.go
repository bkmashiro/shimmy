//go:build linux

package wasm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestReactorPythonDispatcher_OptionalArtifactSmoke(t *testing.T) {
	wasmPath := os.Getenv("PYTHON_REACTOR_WASM")
	if wasmPath == "" {
		t.Skip("PYTHON_REACTOR_WASM not set; skipping optional python-reactor artifact smoke")
	}
	info, err := os.Stat(wasmPath)
	if err != nil {
		t.Skipf("PYTHON_REACTOR_WASM=%q is not readable: %v", wasmPath, err)
	}
	if info.IsDir() {
		t.Skipf("PYTHON_REACTOR_WASM=%q is a directory", wasmPath)
	}

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "eval.py")
	require.NoError(t, os.WriteFile(scriptPath, []byte(`
_invocation_count = 0

def evaluation_function(response, answer, params=None):
    global _invocation_count
    _invocation_count += 1
    return {
        "is_correct": response == answer,
        "feedback": f"count={_invocation_count}",
        "guest_invocation_count": _invocation_count,
        "snapshot_isolation_ok": _invocation_count == 1,
    }
`), 0o644))

	t.Setenv("FUNCTION_WASM_MODULE", wasmPath)
	t.Setenv("FUNCTION_WASM_PYTHON_SCRIPT", scriptPath)
	t.Setenv("FUNCTION_WASM_MAX_MEMORY_PAGES", "8192")
	t.Setenv("FUNCTION_WASM_COMPILE_CACHE", filepath.Join(dir, "wazero-cache"))

	dispatcher := NewReactorPythonDispatcher(Config{
		MaxInstances:    1,
		Timeout:         90 * time.Second,
		MaxMemoryPages:  8192,
		CompileCacheDir: filepath.Join(dir, "wazero-cache"),
	}, zap.NewNop())

	startCtx, cancelStart := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelStart()
	require.NoError(t, dispatcher.Start(startCtx))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = dispatcher.Shutdown(ctx)
	})

	for i, input := range []map[string]any{
		{"response": "42", "answer": "42", "params": map[string]any{}},
		{"response": "41", "answer": "42", "params": map[string]any{}},
	} {
		reqCtx, cancelReq := context.WithTimeout(context.Background(), 90*time.Second)
		got, err := dispatcher.Send(reqCtx, "eval", input)
		cancelReq()
		require.NoError(t, err, "request %d", i+1)

		result, ok := got["result"].(map[string]any)
		require.True(t, ok, "dispatcher response should wrap guest result")
		require.Equal(t, float64(1), result["guest_invocation_count"], "request %d should see a fresh interpreter snapshot", i+1)
		require.Equal(t, true, result["snapshot_isolation_ok"])
	}
}

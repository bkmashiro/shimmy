//go:build linux

package execution

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
	"go.uber.org/zap"
)

func TestPythonReactorPackageEntrypoint_OptionalArtifactSmoke(t *testing.T) {
	wasmPath := os.Getenv("PYTHON_REACTOR_WASM")
	if wasmPath == "" {
		t.Skip("PYTHON_REACTOR_WASM not set; skipping optional python-reactor package-mode artifact smoke")
	}
	if info, err := os.Stat(wasmPath); err != nil || info.IsDir() {
		t.Skipf("PYTHON_REACTOR_WASM=%q is not a readable file", wasmPath)
	}

	root := t.TempDir()
	pkg := filepath.Join(root, "evaluation_function")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "__init__.py"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "evaluation.py"), []byte(`
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
`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FUNCTION_WASM_PROFILE", "python-reactor")
	t.Setenv("FUNCTION_WASM_MODULE", wasmPath)
	t.Setenv("FUNCTION_WASM_PYTHON_SCRIPT", "")
	t.Setenv("FUNCTION_WASM_MAX_MEMORY_PAGES", "8192")
	t.Setenv("FUNCTION_WASM_COMPILE_CACHE", filepath.Join(t.TempDir(), "wazero-cache"))
	t.Setenv("FUNCTION_LF_ROOT", root)
	t.Setenv("FUNCTION_LF_EVAL_ENTRYPOINT", "evaluation_function.evaluation:evaluation_function")
	t.Setenv("FUNCTION_LF_PREVIEW_ENTRYPOINT", "")

	d, err := NewDispatcher(Params{
		Context: context.Background(),
		Config: Config{
			MaxWorkers: 1,
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{Interface: supervisor.WasmIO},
				SendParams: supervisor.SendConfig{
					Timeout: 90 * time.Second,
				},
			},
		},
		Log: zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}

	startCtx, cancelStart := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelStart()
	if err := d.Start(startCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = d.Shutdown(ctx)
	})

	for i, input := range []map[string]any{
		{"response": "42", "answer": "42", "params": map[string]any{}},
		{"response": "41", "answer": "42", "params": map[string]any{}},
	} {
		reqCtx, cancelReq := context.WithTimeout(context.Background(), 90*time.Second)
		got, err := d.Send(reqCtx, "eval", input)
		cancelReq()
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		result, ok := got["result"].(map[string]any)
		if !ok {
			t.Fatalf("request %d response should wrap result, got %#v", i+1, got)
		}
		if result["guest_invocation_count"] != float64(1) {
			t.Fatalf("request %d should see fresh package state, got %#v", i+1, result)
		}
		if result["snapshot_isolation_ok"] != true {
			t.Fatalf("request %d should report snapshot isolation, got %#v", i+1, result)
		}
	}
}

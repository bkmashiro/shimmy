package execution

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

func TestPyodidePythonExample_RunsThroughDispatcher(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for the Pyodide example")
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	exampleDir := filepath.Join(root, "examples", "demo-pyodide-python")
	runner := filepath.Join(exampleDir, "runner.js")
	script := filepath.Join(exampleDir, "eval.py")
	if _, err := os.Stat(filepath.Join(exampleDir, "node_modules", "pyodide")); err != nil {
		t.Skip("Pyodide npm package is not installed; run npm install in examples/demo-pyodide-python")
	}

	t.Setenv("FUNCTION_PYODIDE_RUNNER", runner)
	t.Setenv("FUNCTION_PYODIDE_SCRIPT", script)
	t.Setenv("FUNCTION_PYODIDE_PACKAGES", "")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	d, err := NewDispatcher(Params{
		Context: ctx,
		Config: Config{
			MaxWorkers: 1,
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{Interface: supervisor.PyodideIO},
				SendParams: supervisor.SendConfig{
					Timeout: 60 * time.Second,
				},
				StopParams: supervisor.StopConfig{
					Timeout: 5 * time.Second,
				},
			},
		},
		Log: zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	defer func() { _ = d.Shutdown(context.Background()) }()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	first, err := d.Send(ctx, "eval", map[string]any{
		"response": "42",
		"answer":   "42",
		"params": map[string]any{
			"correct_response_feedback":   "correct via pyodide",
			"incorrect_response_feedback": "incorrect via pyodide",
		},
	})
	if err != nil {
		t.Fatalf("first Send: %v", err)
	}
	second, err := d.Send(ctx, "eval", map[string]any{
		"response": "41",
		"answer":   "42",
		"params":   map[string]any{},
	})
	if err != nil {
		t.Fatalf("second Send: %v", err)
	}

	assertPyodideResult(t, first, true)
	assertPyodideResult(t, second, false)
}

func assertPyodideResult(t *testing.T, got map[string]any, wantCorrect bool) {
	t.Helper()
	if got["command"] != "eval" {
		t.Fatalf("command = %v, want eval; full response: %#v", got["command"], got)
	}
	result, ok := got["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is %T, want map[string]any; full response: %#v", got["result"], got)
	}
	if result["is_correct"] != wantCorrect {
		t.Fatalf("is_correct = %v, want %v; result: %#v", result["is_correct"], wantCorrect, result)
	}
	if result["pyodide_runtime"] != true {
		t.Fatalf("pyodide_runtime = %v, want true; result: %#v", result["pyodide_runtime"], result)
	}
	if result["fresh_namespace_ok"] != true {
		t.Fatalf("fresh_namespace_ok = %v, want true; result: %#v", result["fresh_namespace_ok"], result)
	}
	if result["guest_invocation_count"] != float64(1) {
		t.Fatalf("guest_invocation_count = %v, want 1; result: %#v", result["guest_invocation_count"], result)
	}
}

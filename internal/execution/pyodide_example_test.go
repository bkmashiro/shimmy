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
	root := repoRoot(t)
	exampleDir := filepath.Join(root, "examples", "demo-pyodide-python")
	runner := filepath.Join(exampleDir, "runner.js")
	script := filepath.Join(exampleDir, "eval.py")
	requirePyodideNodeModules(t, exampleDir)

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

func TestPyodidePythonPackageExample_RunsThroughDispatcher(t *testing.T) {
	root := repoRoot(t)
	exampleDir := filepath.Join(root, "examples", "demo-pyodide-python")
	packageRoot := filepath.Join(root, "examples", "demo-pyodide-package")
	adapterPath := filepath.Join(root, "examples", "lambda-feedback-adapter", "lf_compat_adapter.py")

	requireFile(t, filepath.Join(packageRoot, "evaluation_function", "evaluation.py"))
	requireFile(t, filepath.Join(packageRoot, "evaluation_function", "preview.py"))
	requireFile(t, adapterPath)
	requirePyodideNodeModules(t, exampleDir)

	t.Setenv("FUNCTION_PYODIDE_RUNNER", filepath.Join(exampleDir, "runner.js"))
	t.Setenv("FUNCTION_PYODIDE_SCRIPT", "")
	t.Setenv("FUNCTION_PYODIDE_ROOT", packageRoot)
	t.Setenv("FUNCTION_PYODIDE_EVAL_ENTRYPOINT", "evaluation_function.evaluation:evaluation_function")
	t.Setenv("FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT", "evaluation_function.preview:preview_function")
	t.Setenv("FUNCTION_PYODIDE_ADAPTER", adapterPath)
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
			"correct_response_feedback":   "package correct",
			"incorrect_response_feedback": "package incorrect",
		},
	})
	if err != nil {
		t.Fatalf("first eval Send: %v", err)
	}
	second, err := d.Send(ctx, "eval", map[string]any{
		"response": "41",
		"answer":   "42",
		"params":   map[string]any{},
	})
	if err != nil {
		t.Fatalf("second eval Send: %v", err)
	}
	preview, err := d.Send(ctx, "preview", map[string]any{
		"response": "draft answer",
		"answer":   "42",
		"params":   map[string]any{"rubric": "exact match"},
	})
	if err != nil {
		t.Fatalf("preview Send: %v", err)
	}

	assertPyodideResult(t, first, true)
	assertPyodideResult(t, second, false)
	assertPyodidePreviewResult(t, preview)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func requirePyodideNodeModules(t *testing.T, exampleDir string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for the Pyodide example")
	}
	if _, err := os.Stat(filepath.Join(exampleDir, "node_modules", "pyodide")); err != nil {
		t.Skip("Pyodide npm package is not installed; run npm install in examples/demo-pyodide-python")
	}
}

func requireFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("required test fixture is missing: %s: %v", path, err)
	}
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

func assertPyodidePreviewResult(t *testing.T, got map[string]any) {
	t.Helper()
	if got["command"] != "preview" {
		t.Fatalf("command = %v, want preview; full response: %#v", got["command"], got)
	}
	result, ok := got["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is %T, want map[string]any; full response: %#v", got["result"], got)
	}
	if result["preview"] != true {
		t.Fatalf("preview = %v, want true; result: %#v", result["preview"], result)
	}
	if result["fresh_namespace_ok"] != true {
		t.Fatalf("fresh_namespace_ok = %v, want true; result: %#v", result["fresh_namespace_ok"], result)
	}
	if result["guest_invocation_count"] != float64(1) {
		t.Fatalf("guest_invocation_count = %v, want 1; result: %#v", result["guest_invocation_count"], result)
	}
}

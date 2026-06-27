package wasm

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestReactorPythonDispatcher_StartRequiresScript(t *testing.T) {
	t.Setenv("FUNCTION_WASM_MODULE", "/tmp/python-reactor.wasm")
	t.Setenv("FUNCTION_WASM_PYTHON_SCRIPT", "")

	d := NewReactorPythonDispatcher(Config{MaxInstances: 1}, zap.NewNop())
	err := d.Start(context.Background())
	if err == nil {
		t.Fatal("expected missing script error")
	}
	if got, want := err.Error(), "FUNCTION_WASM_PYTHON_SCRIPT"; !strings.Contains(got, want) {
		t.Fatalf("expected error to contain %q, got %q", want, got)
	}
}

func TestReactorPythonDispatcher_StartRequiresModule(t *testing.T) {
	t.Setenv("FUNCTION_WASM_MODULE", "")
	script := filepath.Join(t.TempDir(), "eval.py")
	if err := os.WriteFile(script, []byte("def evaluation_function(response, answer, params):\n    return {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUNCTION_WASM_PYTHON_SCRIPT", script)

	d := NewReactorPythonDispatcher(Config{MaxInstances: 1}, zap.NewNop())
	err := d.Start(context.Background())
	if err == nil {
		t.Fatal("expected missing module error")
	}
	if got, want := err.Error(), "FUNCTION_WASM_MODULE"; !strings.Contains(got, want) {
		t.Fatalf("expected error to contain %q, got %q", want, got)
	}
}

func TestReactorPythonDispatcher_SendBeforeStartFails(t *testing.T) {
	d := NewReactorPythonDispatcher(Config{MaxInstances: 1}, zap.NewNop())

	_, err := d.Send(context.Background(), "eval", map[string]any{"response": "42"})
	if err == nil {
		t.Fatal("expected send-before-start error")
	}
	if got, want := err.Error(), "not been started"; !strings.Contains(got, want) {
		t.Fatalf("expected error to contain %q, got %q", want, got)
	}
}

func TestReactorPythonDispatcher_StartReportsInvalidModuleCompile(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "eval.py")
	module := filepath.Join(dir, "python-reactor.wasm")
	if err := os.WriteFile(script, []byte("def evaluation_function(response, answer, params):\n    return {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(module, []byte("not a wasm module"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FUNCTION_WASM_MODULE", module)
	t.Setenv("FUNCTION_WASM_PYTHON_SCRIPT", script)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d := NewReactorPythonDispatcher(Config{MaxInstances: 1}, zap.NewNop())
	err := d.Start(ctx)
	if err == nil {
		t.Fatal("expected invalid module compile error")
	}
	if got := err.Error(); runtime.GOOS == "linux" {
		if want := "compile module"; !strings.Contains(got, want) {
			t.Fatalf("expected error to contain %q, got %q", want, got)
		}
	} else {
		if want := "python-reactor env module is only supported on Linux"; !strings.Contains(got, want) {
			t.Fatalf("expected error to contain %q, got %q", want, got)
		}
	}
}

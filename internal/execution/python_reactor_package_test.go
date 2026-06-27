package execution

import (
	"os"
	"strings"
	"testing"

	"github.com/lambda-feedback/shimmy/internal/execution/wasm"
)

func TestValidatePythonReactorConfigAcceptsPackageEntrypoint(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FUNCTION_WASM_MODULE", "/tmp/python-reactor.wasm")
	t.Setenv("FUNCTION_WASM_PYTHON_SCRIPT", "")
	t.Setenv("FUNCTION_LF_ROOT", root)
	t.Setenv("FUNCTION_LF_EVAL_ENTRYPOINT", "evaluation_function.evaluation:evaluation_function")
	t.Setenv("FUNCTION_LF_PREVIEW_ENTRYPOINT", "evaluation_function.preview:preview_function")

	cfg := wasm.Config{}
	if err := validatePythonReactorConfig(&cfg); err != nil {
		t.Fatalf("expected package-mode config to pass, got %v", err)
	}
	if cfg.PythonScriptPath == "" {
		t.Fatal("expected generated Python script path")
	}
	if _, err := os.Stat(cfg.PythonScriptPath); err != nil {
		t.Fatalf("expected generated Python script to exist: %v", err)
	}
	if len(cfg.AllowedPaths) != 1 || cfg.AllowedPaths[0] != root {
		t.Fatalf("expected package root to be mounted read-only, got %#v", cfg.AllowedPaths)
	}
	bundle, err := os.ReadFile(cfg.PythonScriptPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(bundle)
	for _, want := range []string{root, "evaluation_function.evaluation:evaluation_function", "evaluation_function.preview:preview_function"} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated script missing %q:\n%s", want, text)
		}
	}
}

func TestValidatePythonReactorConfigRequiresScriptOrPackageEntrypoint(t *testing.T) {
	t.Setenv("FUNCTION_WASM_MODULE", "/tmp/python-reactor.wasm")
	t.Setenv("FUNCTION_WASM_PYTHON_SCRIPT", "")
	t.Setenv("FUNCTION_LF_ROOT", "")
	t.Setenv("FUNCTION_LF_EVAL_ENTRYPOINT", "")

	cfg := wasm.Config{}
	err := validatePythonReactorConfig(&cfg)
	if err == nil {
		t.Fatal("expected config error")
	}
	if got, want := err.Error(), "FUNCTION_WASM_PYTHON_SCRIPT"; !strings.Contains(got, want) {
		t.Fatalf("expected error to contain %q, got %q", want, got)
	}
	if got, want := err.Error(), "FUNCTION_LF_ROOT"; !strings.Contains(got, want) {
		t.Fatalf("expected error to contain %q, got %q", want, got)
	}
}

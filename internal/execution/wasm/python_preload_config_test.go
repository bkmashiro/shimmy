package wasm

import "testing"

func TestPythonPreloadModeDefaultsToEvaluator(t *testing.T) {
	var cfg Config
	cfg.applyDefaults()
	if cfg.PythonPreloadMode != "evaluator" {
		t.Fatalf("default preload mode = %q, want evaluator", cfg.PythonPreloadMode)
	}
}

func TestPythonPreloadModeCanBeDisabled(t *testing.T) {
	t.Setenv("FUNCTION_WASM_PYTHON_PRELOAD", "off")
	var cfg Config
	cfg.applyEnv()
	cfg.applyDefaults()
	if cfg.PythonPreloadMode != "off" {
		t.Fatalf("preload mode = %q, want off", cfg.PythonPreloadMode)
	}
}

func TestPythonPreloadModeRejectsUnknownValue(t *testing.T) {
	cfg := Config{PythonPreloadMode: "typo"}
	if err := cfg.validatePythonPreloadMode(); err == nil {
		t.Fatal("unknown preload mode must fail closed")
	}
}

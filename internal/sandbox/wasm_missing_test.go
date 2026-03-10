package sandbox

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestWasmBackend_WrapCommandMissingExplicitWasmFileInWorkDir(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	missing := filepath.Join(workDir, "missing module.wasm")

	backend := &WasmBackend{wasmtimePath: writeExecutable(t, "wasmtime")}
	_, err := backend.WrapCommand(context.Background(), missing, []string{"--verbose"}, Config{WorkDir: workDir})
	if err == nil {
		t.Fatal("WrapCommand() error = nil, want missing wasm error")
	}
	if !strings.Contains(err.Error(), "wasm program not found: "+missing) {
		t.Fatalf("WrapCommand() error = %q, want path %q in error", err.Error(), missing)
	}
}

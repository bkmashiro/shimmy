//go:build integration

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandlockBackend_RunCommand(t *testing.T) {
	backend := &SandlockBackend{}
	if !backend.Available() {
		t.Skip("sandlock binary not available")
	}

	cmd, err := backend.WrapCommand(context.Background(), "sh", []string{"-c", "printf ok"}, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}
	if string(out) != "ok" {
		t.Fatalf("output = %q, want %q", string(out), "ok")
	}
}

func TestSandlockBackend_EnforcesCPUTimeout(t *testing.T) {
	backend := &SandlockBackend{}
	if !backend.Available() {
		t.Skip("sandlock binary not available")
	}

	cmd, err := backend.WrapCommand(context.Background(), "sh", []string{"-c", "while :; do :; done"}, Config{
		CPUTimeSecs: 1,
		MaxMemoryMB: 64,
	})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	err = cmd.Run()
	if err == nil {
		t.Fatal("Run() error = nil, want non-nil")
	}
}

func TestSandlockBackend_DisablesNetworkFlag(t *testing.T) {
	backend := &SandlockBackend{}
	if !backend.Available() {
		t.Skip("sandlock binary not available")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd, err := backend.WrapCommand(ctx, "curl", []string{"-I", "https://example.com"}, Config{
		CPUTimeSecs: 2,
		MaxMemoryMB: 128,
	})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	err = cmd.Run()
	if err == nil {
		t.Fatal("Run() error = nil, want non-nil")
	}
}

func TestWasmBackend_RunCommand(t *testing.T) {
	backend := &WasmBackend{}
	if !backend.Available() {
		t.Skip("wasmtime binary not available")
	}

	dir := t.TempDir()
	program := buildTestWasm(t, dir)

	cmd, err := backend.WrapCommand(context.Background(), program, nil, Config{WorkDir: dir})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}
	if string(out) != "hello from wasm\n" {
		t.Fatalf("output = %q, want %q", string(out), "hello from wasm\n")
	}
}

func TestWasmBackend_ImplicitProgramLookup(t *testing.T) {
	backend := &WasmBackend{}
	if !backend.Available() {
		t.Skip("wasmtime binary not available")
	}

	dir := t.TempDir()
	_ = buildTestWasm(t, dir)

	cmd, err := backend.WrapCommand(context.Background(), "hello", nil, Config{WorkDir: dir})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}
	if string(out) != "hello from wasm\n" {
		t.Fatalf("output = %q, want %q", string(out), "hello from wasm\n")
	}
}

func TestWasmBackend_MissingProgramFails(t *testing.T) {
	backend := &WasmBackend{}
	if !backend.Available() {
		t.Skip("wasmtime binary not available")
	}

	_, err := backend.WrapCommand(context.Background(), "missing", nil, Config{WorkDir: t.TempDir()})
	if err == nil {
		t.Fatal("WrapCommand() error = nil, want non-nil")
	}
}

func buildTestWasm(t *testing.T, dir string) string {
	t.Helper()

	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	source := filepath.Join(dir, "hello.c")
	program := filepath.Join(dir, "hello.wasm")
	code := strings.Join([]string{
		"#include <stdio.h>",
		"int main(void) {",
		`  puts("hello from wasm");`,
		"  return 0;",
		"}",
	}, "\n")
	if err := os.WriteFile(source, []byte(code), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := exec.Command(
		"clang",
		"--target=wasm32-wasi",
		"-O2",
		"-Wl,--export-all",
		"-Wl,--no-entry",
		"-o",
		program,
		source,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("failed to build test wasm: %s", string(out))
	}

	return program
}

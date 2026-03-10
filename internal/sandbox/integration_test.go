//go:build integration

package sandbox

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================
// DirectBackend integration tests
// ============================================================

func TestDirectBackend_RunEchoHello(t *testing.T) {
	backend := &DirectBackend{}

	cmd, err := backend.WrapCommand(context.Background(), "echo", []string{"hello"}, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}
	if strings.TrimSpace(string(out)) != "hello" {
		t.Fatalf("output = %q, want %q", string(out), "hello")
	}
}

func TestDirectBackend_RunCatStdin(t *testing.T) {
	backend := &DirectBackend{}

	cmd, err := backend.WrapCommand(context.Background(), "cat", nil, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	cmd.Stdin = bytes.NewBufferString("hello from stdin")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}
	if string(out) != "hello from stdin" {
		t.Fatalf("output = %q, want %q", string(out), "hello from stdin")
	}
}

func TestDirectBackend_CompletesWithinTimeout(t *testing.T) {
	backend := &DirectBackend{}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd, err := backend.WrapCommand(ctx, "echo", []string{"done"}, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}
	if strings.TrimSpace(string(out)) != "done" {
		t.Fatalf("output = %q, want %q", string(out), "done")
	}
}

func TestDirectBackend_StderrCapture(t *testing.T) {
	backend := &DirectBackend{}

	cmd, err := backend.WrapCommand(context.Background(), "sh", []string{"-c", "echo err >&2"}, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.TrimSpace(stderr.String()) != "err" {
		t.Fatalf("stderr = %q, want %q", stderr.String(), "err")
	}
}

func TestDirectBackend_NonZeroExitCode(t *testing.T) {
	backend := &DirectBackend{}

	cmd, err := backend.WrapCommand(context.Background(), "sh", []string{"-c", "exit 42"}, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	err = cmd.Run()
	if err == nil {
		t.Fatal("Run() error = nil, want non-nil for exit code 42")
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 42 {
			t.Fatalf("exit code = %d, want 42", exitErr.ExitCode())
		}
	}
}

func TestDirectBackend_PipedIO(t *testing.T) {
	backend := &DirectBackend{}

	cmd, err := backend.WrapCommand(context.Background(), "cat", nil, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	msg := "piped message"
	if _, err := io.WriteString(stdin, msg); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	stdin.Close()

	out, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if string(out) != msg {
		t.Fatalf("output = %q, want %q", string(out), msg)
	}
}

func TestDirectBackend_ContextCancellation(t *testing.T) {
	backend := &DirectBackend{}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	cmd, err := backend.WrapCommand(ctx, "sleep", []string{"30"}, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	err = cmd.Run()
	if err == nil {
		t.Fatal("Run() error = nil, want non-nil (context should cancel)")
	}
}

// ============================================================
// SandlockBackend integration tests
// ============================================================

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

func TestSandlockBackend_RunEchoSandlocked(t *testing.T) {
	backend := &SandlockBackend{}
	if !backend.Available() {
		t.Skip("sandlock binary not available")
	}

	cmd, err := backend.WrapCommand(context.Background(), "echo", []string{"sandlocked"}, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}
	if strings.TrimSpace(string(out)) != "sandlocked" {
		t.Fatalf("output = %q, want %q", string(out), "sandlocked")
	}
}

func TestSandlockBackend_EnforcesCPUTimeout(t *testing.T) {
	backend := &SandlockBackend{}
	if !backend.Available() {
		t.Skip("sandlock binary not available")
	}

	start := time.Now()
	cmd, err := backend.WrapCommand(context.Background(), "sh", []string{"-c", "while :; do :; done"}, Config{
		CPUTimeSecs: 1,
		MaxMemoryMB: 64,
	})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	err = cmd.Run()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Run() error = nil, want non-nil")
	}
	// Should complete reasonably quickly (within 10s wallclock for 1s CPU limit)
	if elapsed > 10*time.Second {
		t.Fatalf("took %v, expected completion within 10s for 1s CPU limit", elapsed)
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

func TestSandlockBackend_StdinStdout(t *testing.T) {
	backend := &SandlockBackend{}
	if !backend.Available() {
		t.Skip("sandlock binary not available")
	}

	cmd, err := backend.WrapCommand(context.Background(), "cat", nil, Config{
		CPUTimeSecs:  5,
		MaxMemoryMB:  64,
		AllowNetwork: true,
	})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	cmd.Stdin = bytes.NewBufferString("sandlock stdin test")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}
	if string(out) != "sandlock stdin test" {
		t.Fatalf("output = %q, want %q", string(out), "sandlock stdin test")
	}
}

func TestSandlockBackend_WithWorkDir(t *testing.T) {
	backend := &SandlockBackend{}
	if !backend.Available() {
		t.Skip("sandlock binary not available")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "testfile.txt"), []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd, err := backend.WrapCommand(context.Background(), "cat", []string{"testfile.txt"}, Config{
		CPUTimeSecs:  5,
		MaxMemoryMB:  64,
		AllowNetwork: true,
		WorkDir:      dir,
	})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}
	if string(out) != "content" {
		t.Fatalf("output = %q, want %q", string(out), "content")
	}
}

// ============================================================
// WasmBackend integration tests
// ============================================================

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

func TestWasmBackend_RunWithFuel(t *testing.T) {
	backend := &WasmBackend{}
	if !backend.Available() {
		t.Skip("wasmtime binary not available")
	}

	dir := t.TempDir()
	program := buildTestWasm(t, dir)

	cmd, err := backend.WrapCommand(context.Background(), program, nil, Config{
		WorkDir:     dir,
		CPUTimeSecs: 5,
	})
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

// ============================================================
// helpers
// ============================================================

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

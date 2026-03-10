package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDirectBackend_WrapCommand(t *testing.T) {
	t.Parallel()

	backend := &DirectBackend{}
	cmd, err := backend.WrapCommand(context.Background(), "echo", []string{"hello"}, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	if filepath.Base(cmd.Path) != "echo" {
		t.Fatalf("filepath.Base(cmd.Path) = %q, want %q", filepath.Base(cmd.Path), "echo")
	}
	if !reflect.DeepEqual(cmd.Args, []string{"echo", "hello"}) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, []string{"echo", "hello"})
	}
}

func TestDirectBackend_Metadata(t *testing.T) {
	t.Parallel()

	backend := &DirectBackend{}
	if !backend.Available() {
		t.Fatal("Available() = false, want true")
	}
	if backend.Name() != "direct" {
		t.Fatalf("Name() = %q, want %q", backend.Name(), "direct")
	}
}

func TestDirectBackend_WrapCommandWithoutArgs(t *testing.T) {
	t.Parallel()

	backend := &DirectBackend{}
	cmd, err := backend.WrapCommand(context.Background(), "pwd", nil, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	if !reflect.DeepEqual(cmd.Args, []string{"pwd"}) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, []string{"pwd"})
	}
}

func TestSandlockBackend_Available(t *testing.T) {
	t.Parallel()

	backend := &SandlockBackend{}
	if !backend.Available() {
		t.Skip("sandlock binary not available")
	}

	if backend.Name() != "sandlock" {
		t.Fatalf("Name() = %q, want %q", backend.Name(), "sandlock")
	}
}

func TestSandlockBackend_CommandArgs(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cfg := Config{
		MaxMemoryMB:  512,
		CPUTimeSecs:  7,
		AllowNetwork: false,
		WorkDir:      "/work",
	}

	cmd, err := backend.WrapCommand(context.Background(), "python3", []string{"main.py"}, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	if cmd.Path != bin {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, bin)
	}
	want := []string{
		bin,
		"--cpu", "7",
		"--timeout", "7",
		"--mem", "512",
		"--no-network",
		"--workdir", "/work",
		"--",
		"python3",
		"main.py",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, want)
	}
}

func TestSandlockBackend_CommandArgsAllowNetwork(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cfg := Config{
		MaxMemoryMB:  128,
		CPUTimeSecs:  0,
		AllowNetwork: true,
	}

	cmd, err := backend.WrapCommand(context.Background(), "sh", []string{"-c", "true"}, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	want := []string{
		bin,
		"--mem", "128",
		"--",
		"sh",
		"-c",
		"true",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, want)
	}
}

func TestWasmBackend_Available(t *testing.T) {
	t.Parallel()

	backend := &WasmBackend{}
	if !backend.Available() {
		t.Skip("wasmtime binary not available")
	}

	if backend.Name() != "wasm" {
		t.Fatalf("Name() = %q, want %q", backend.Name(), "wasm")
	}
}

func TestWasmBackend_CommandArgsWithExplicitProgram(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	program := filepath.Join(dir, "demo.wasm")
	if err := os.WriteFile(program, []byte("wasm"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	bin := writeExecutable(t, "wasmtime")
	backend := &WasmBackend{wasmtimePath: bin}
	cfg := Config{
		CPUTimeSecs: 3,
		WorkDir:     dir,
	}

	cmd, err := backend.WrapCommand(context.Background(), program, []string{"arg1"}, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	want := []string{
		bin,
		"run",
		"--dir", dir,
		"--fuel", "3000000",
		"--",
		program,
		"arg1",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, want)
	}
}

func TestWasmBackend_CommandArgsWithImplicitProgram(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	program := filepath.Join(dir, "demo.wasm")
	if err := os.WriteFile(program, []byte("wasm"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	bin := writeExecutable(t, "wasmtime")
	backend := &WasmBackend{wasmtimePath: bin}
	cmd, err := backend.WrapCommand(context.Background(), "demo", []string{"arg1", "arg2"}, Config{WorkDir: dir})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	want := []string{
		bin,
		"run",
		"--dir", dir,
		"--",
		program,
		"arg1",
		"arg2",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, want)
	}
}

func TestWasmBackend_CommandArgsMissingProgram(t *testing.T) {
	t.Parallel()

	backend := &WasmBackend{wasmtimePath: writeExecutable(t, "wasmtime")}
	_, err := backend.WrapCommand(context.Background(), "missing", nil, Config{WorkDir: t.TempDir()})
	if err == nil {
		t.Fatal("WrapCommand() error = nil, want non-nil")
	}
}

func TestNewBackend(t *testing.T) {
	t.Parallel()

	if _, ok := NewBackend("direct").(*DirectBackend); !ok {
		t.Fatal("NewBackend(direct) did not return *DirectBackend")
	}
	if _, ok := NewBackend("unknown").(*DirectBackend); !ok {
		t.Fatal("NewBackend(unknown) did not return *DirectBackend")
	}
}

func TestNewBackendFromEnv(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "direct")
	if _, ok := NewBackendFromEnv().(*DirectBackend); !ok {
		t.Fatal("NewBackendFromEnv() did not return *DirectBackend")
	}
}

func TestSandlockBackend_ResolveBinaryPrefersPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "sandlock")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	t.Setenv("PATH", dir)
	backend := &SandlockBackend{}

	resolved, err := backend.resolveBinary()
	if err != nil {
		t.Fatalf("resolveBinary() error = %v", err)
	}
	if resolved != "sandlock" {
		t.Fatalf("resolveBinary() = %q, want %q", resolved, "sandlock")
	}
}

func TestWasmBackend_ResolveBinaryUsesConfiguredPath(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "custom-wasmtime")
	backend := &WasmBackend{wasmtimePath: bin}
	resolved, err := backend.resolveBinary()
	if err != nil {
		t.Fatalf("resolveBinary() error = %v", err)
	}
	if resolved != bin {
		t.Fatalf("resolveBinary() = %q, want %q", resolved, bin)
	}
}

func writeExecutable(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

var _ SandboxBackend = (*DirectBackend)(nil)
var _ SandboxBackend = (*SandlockBackend)(nil)
var _ SandboxBackend = (*WasmBackend)(nil)

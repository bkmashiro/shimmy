package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestSandlockBackend_WrapCommandCPUOnly(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cfg := Config{CPUTimeSecs: 5}

	cmd, err := backend.WrapCommand(context.Background(), "ls", nil, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	want := []string{bin, "--cpu", "5", "--timeout", "5", "--no-network", "--", "ls"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, want)
	}
}

func TestSandlockBackend_WrapCommandMemOnly(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cfg := Config{MaxMemoryMB: 1024}

	cmd, err := backend.WrapCommand(context.Background(), "ls", nil, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	want := []string{bin, "--mem", "1024", "--no-network", "--", "ls"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, want)
	}
}

func TestSandlockBackend_WrapCommandTimeoutMatchesCPU(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cfg := Config{CPUTimeSecs: 30, MaxMemoryMB: 256, AllowNetwork: true}

	cmd, err := backend.WrapCommand(context.Background(), "python3", []string{"run.py"}, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	// --timeout should equal --cpu
	cpuIdx := -1
	timeoutIdx := -1
	for i, a := range cmd.Args {
		if a == "--cpu" {
			cpuIdx = i
		}
		if a == "--timeout" {
			timeoutIdx = i
		}
	}
	if cpuIdx < 0 || timeoutIdx < 0 {
		t.Fatalf("missing --cpu or --timeout in args: %v", cmd.Args)
	}
	if cmd.Args[cpuIdx+1] != cmd.Args[timeoutIdx+1] {
		t.Fatalf("--cpu %s != --timeout %s", cmd.Args[cpuIdx+1], cmd.Args[timeoutIdx+1])
	}
}

func TestSandlockBackend_WrapCommandWorkDir(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cfg := Config{WorkDir: "/home/user/project", AllowNetwork: true}

	cmd, err := backend.WrapCommand(context.Background(), "node", []string{"index.js"}, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	// Check --workdir is present with the correct value
	found := false
	for i, a := range cmd.Args {
		if a == "--workdir" && i+1 < len(cmd.Args) && cmd.Args[i+1] == "/home/user/project" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("--workdir /home/user/project not found in args: %v", cmd.Args)
	}
}

func TestSandlockBackend_WrapCommandNoWorkDir(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cfg := Config{AllowNetwork: true} // No WorkDir set

	cmd, err := backend.WrapCommand(context.Background(), "ls", nil, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	for _, a := range cmd.Args {
		if a == "--workdir" {
			t.Fatal("--workdir should not be present when WorkDir is empty")
		}
	}
}

func TestSandlockBackend_WrapCommandZeroCPUOmitsFlag(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cfg := Config{CPUTimeSecs: 0, MaxMemoryMB: 128, AllowNetwork: true}

	cmd, err := backend.WrapCommand(context.Background(), "echo", []string{"ok"}, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	for _, a := range cmd.Args {
		if a == "--cpu" || a == "--timeout" {
			t.Fatalf("--cpu / --timeout should not be present when CPUTimeSecs=0, args: %v", cmd.Args)
		}
	}
}

func TestSandlockBackend_WrapCommandZeroMemOmitsFlag(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cfg := Config{MaxMemoryMB: 0, AllowNetwork: true}

	cmd, err := backend.WrapCommand(context.Background(), "echo", nil, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	for _, a := range cmd.Args {
		if a == "--mem" {
			t.Fatalf("--mem should not be present when MaxMemoryMB=0, args: %v", cmd.Args)
		}
	}
}

func TestSandlockBackend_WrapCommandNetworkBlocked(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cfg := Config{AllowNetwork: false}

	cmd, err := backend.WrapCommand(context.Background(), "curl", nil, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	found := false
	for _, a := range cmd.Args {
		if a == "--no-network" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("--no-network should be present when AllowNetwork=false, args: %v", cmd.Args)
	}
}

func TestSandlockBackend_WrapCommandAllParamsPresent(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cfg := Config{
		MaxMemoryMB:  2048,
		CPUTimeSecs:  60,
		AllowNetwork: false,
		WorkDir:      "/sandbox/run",
	}

	cmd, err := backend.WrapCommand(context.Background(), "ruby", []string{"script.rb", "--verbose"}, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	want := []string{
		bin,
		"--cpu", "60", "--timeout", "60",
		"--mem", "2048",
		"--no-network",
		"--workdir", "/sandbox/run",
		"--",
		"ruby", "script.rb", "--verbose",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, want)
	}
}

func TestSandlockBackend_ResolveBinaryFromEnv(t *testing.T) {
	bin := writeExecutable(t, "my-sandlock")
	t.Setenv("SHIMMY_SANDLOCK_PATH", bin)
	t.Setenv("PATH", "") // clear PATH to ensure env var takes precedence

	backend := &SandlockBackend{}
	resolved, err := backend.resolveBinary()
	if err != nil {
		t.Fatalf("resolveBinary() error = %v", err)
	}
	if resolved != bin {
		t.Fatalf("resolveBinary() = %q, want %q", resolved, bin)
	}
}

func TestSandlockBackend_ResolveBinaryConfiguredPathTakesPrecedence(t *testing.T) {
	bin := writeExecutable(t, "sandlock-custom")
	envBin := writeExecutable(t, "sandlock-env")
	t.Setenv("SHIMMY_SANDLOCK_PATH", envBin)

	backend := &SandlockBackend{binaryPath: bin}
	resolved, err := backend.resolveBinary()
	if err != nil {
		t.Fatalf("resolveBinary() error = %v", err)
	}
	if resolved != bin {
		t.Fatalf("resolveBinary() = %q, want %q (configured path should take precedence over env)", resolved, bin)
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

// --- WasmBackend extended tests ---

func TestWasmBackend_WrapCommandExplicitWasmExtension(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	program := filepath.Join(dir, "module.wasm")
	if err := os.WriteFile(program, []byte("wasm"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	bin := writeExecutable(t, "wasmtime")
	backend := &WasmBackend{wasmtimePath: bin}
	cmd, err := backend.WrapCommand(context.Background(), program, nil, Config{WorkDir: dir})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	// Should use program path as-is (already ends in .wasm)
	found := false
	for _, a := range cmd.Args {
		if a == program {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected program %q in args: %v", program, cmd.Args)
	}
}

func TestWasmBackend_WrapCommandImplicitWasmSuffix(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	wasmFile := filepath.Join(dir, "app.wasm")
	if err := os.WriteFile(wasmFile, []byte("wasm"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	bin := writeExecutable(t, "wasmtime")
	backend := &WasmBackend{wasmtimePath: bin}
	cmd, err := backend.WrapCommand(context.Background(), "app", nil, Config{WorkDir: dir})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	// Should resolve to app.wasm in workdir
	found := false
	for _, a := range cmd.Args {
		if a == wasmFile {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected resolved program %q in args: %v", wasmFile, cmd.Args)
	}
}

func TestWasmBackend_WrapCommandFuelCalculation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	program := filepath.Join(dir, "run.wasm")
	if err := os.WriteFile(program, []byte("wasm"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	bin := writeExecutable(t, "wasmtime")
	backend := &WasmBackend{wasmtimePath: bin}

	tests := []struct {
		cpuSecs  int
		wantFuel string
	}{
		{1, "1000000"},
		{5, "5000000"},
		{10, "10000000"},
		{100, "100000000"},
	}

	for _, tc := range tests {
		cfg := Config{CPUTimeSecs: tc.cpuSecs, WorkDir: dir}
		cmd, err := backend.WrapCommand(context.Background(), program, nil, cfg)
		if err != nil {
			t.Fatalf("WrapCommand(cpu=%d) error = %v", tc.cpuSecs, err)
		}
		fuelFound := false
		for i, a := range cmd.Args {
			if a == "--fuel" && i+1 < len(cmd.Args) {
				if cmd.Args[i+1] != tc.wantFuel {
					t.Fatalf("cpu=%d: --fuel = %s, want %s", tc.cpuSecs, cmd.Args[i+1], tc.wantFuel)
				}
				fuelFound = true
				break
			}
		}
		if !fuelFound {
			t.Fatalf("cpu=%d: --fuel not found in args: %v", tc.cpuSecs, cmd.Args)
		}
	}
}

func TestWasmBackend_WrapCommandZeroCPUOmitsFuel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	program := filepath.Join(dir, "run.wasm")
	if err := os.WriteFile(program, []byte("wasm"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	bin := writeExecutable(t, "wasmtime")
	backend := &WasmBackend{wasmtimePath: bin}
	cmd, err := backend.WrapCommand(context.Background(), program, nil, Config{WorkDir: dir})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	for _, a := range cmd.Args {
		if a == "--fuel" {
			t.Fatalf("--fuel should not be present when CPUTimeSecs=0, args: %v", cmd.Args)
		}
	}
}

func TestWasmBackend_WrapCommandNoWorkDirUsesCurrentDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	program := filepath.Join(dir, "run.wasm")
	if err := os.WriteFile(program, []byte("wasm"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	bin := writeExecutable(t, "wasmtime")
	backend := &WasmBackend{wasmtimePath: bin}
	cmd, err := backend.WrapCommand(context.Background(), program, nil, Config{})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	// When WorkDir is empty, should use --dir .
	dirFound := false
	for i, a := range cmd.Args {
		if a == "--dir" && i+1 < len(cmd.Args) && cmd.Args[i+1] == "." {
			dirFound = true
			break
		}
	}
	if !dirFound {
		t.Fatalf("expected --dir . when WorkDir is empty, args: %v", cmd.Args)
	}
}

func TestWasmBackend_WrapCommandWithWorkDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	program := filepath.Join(dir, "run.wasm")
	if err := os.WriteFile(program, []byte("wasm"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	bin := writeExecutable(t, "wasmtime")
	backend := &WasmBackend{wasmtimePath: bin}
	cmd, err := backend.WrapCommand(context.Background(), program, nil, Config{WorkDir: dir})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	dirFound := false
	for i, a := range cmd.Args {
		if a == "--dir" && i+1 < len(cmd.Args) && cmd.Args[i+1] == dir {
			dirFound = true
			break
		}
	}
	if !dirFound {
		t.Fatalf("expected --dir %s, args: %v", dir, cmd.Args)
	}
}

func TestWasmBackend_ResolveBinaryFromEnv(t *testing.T) {
	bin := writeExecutable(t, "my-wasmtime")
	t.Setenv("SHIMMY_WASMTIME_PATH", bin)

	backend := &WasmBackend{}
	resolved, err := backend.resolveBinary()
	if err != nil {
		t.Fatalf("resolveBinary() error = %v", err)
	}
	if resolved != bin {
		t.Fatalf("resolveBinary() = %q, want %q", resolved, bin)
	}
}

func TestWasmBackend_ResolveBinaryConfiguredPathTakesPrecedence(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "wasmtime-custom")
	backend := &WasmBackend{wasmtimePath: bin}

	resolved, err := backend.resolveBinary()
	if err != nil {
		t.Fatalf("resolveBinary() error = %v", err)
	}
	if resolved != bin {
		t.Fatalf("resolveBinary() = %q, want %q", resolved, bin)
	}
}

func TestWasmBackend_ResolveProgramWithAbsolutePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	absWasm := filepath.Join(dir, "absolute.wasm")
	if err := os.WriteFile(absWasm, []byte("wasm"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	bin := writeExecutable(t, "wasmtime")
	backend := &WasmBackend{wasmtimePath: bin}
	cmd, err := backend.WrapCommand(context.Background(), absWasm, nil, Config{WorkDir: "/other"})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	// Absolute .wasm path should be used as-is
	found := false
	for _, a := range cmd.Args {
		if a == absWasm {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected absolute program %q in args: %v", absWasm, cmd.Args)
	}
}

func TestWasmBackend_WrapCommandRunSubcommand(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	program := filepath.Join(dir, "test.wasm")
	if err := os.WriteFile(program, []byte("wasm"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	bin := writeExecutable(t, "wasmtime")
	backend := &WasmBackend{wasmtimePath: bin}
	cmd, err := backend.WrapCommand(context.Background(), program, nil, Config{WorkDir: dir})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	// First arg after binary should be "run"
	if len(cmd.Args) < 2 || cmd.Args[1] != "run" {
		t.Fatalf("expected 'run' subcommand, args: %v", cmd.Args)
	}
}

// --- Factory extended tests ---

func TestNewBackend_Direct(t *testing.T) {
	t.Parallel()

	b := NewBackend("direct")
	if _, ok := b.(*DirectBackend); !ok {
		t.Fatalf("NewBackend(\"direct\") returned %T, want *DirectBackend", b)
	}
}

func TestNewBackend_UnknownFallsToDirect(t *testing.T) {
	t.Parallel()

	b := NewBackend("unknown-backend")
	if _, ok := b.(*DirectBackend); !ok {
		t.Fatalf("NewBackend(\"unknown-backend\") returned %T, want *DirectBackend", b)
	}
}

func TestNewBackend_EmptyFallsToDirect(t *testing.T) {
	t.Parallel()

	b := NewBackend("")
	if _, ok := b.(*DirectBackend); !ok {
		t.Fatalf("NewBackend(\"\") returned %T, want *DirectBackend", b)
	}
}

func TestNewBackend_SandlockFallsBackOnNonLinux(t *testing.T) {
	t.Parallel()

	b := NewBackend("sandlock")
	// On non-Linux (macOS/Darwin), sandlock is unavailable and should fall back
	if b.Name() == "sandlock" && !b.Available() {
		t.Fatal("returned sandlock backend that is not available")
	}
	// Either returns sandlock (on Linux) or direct (fallback)
}

func TestNewBackend_WasmFallsBackWithoutWasmtime(t *testing.T) {
	t.Parallel()

	// Clear PATH to make wasmtime unavailable
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	os.Unsetenv("SHIMMY_WASMTIME_PATH")

	b := NewBackend("wasm")
	if _, ok := b.(*DirectBackend); !ok {
		t.Fatalf("NewBackend(\"wasm\") without wasmtime returned %T, want *DirectBackend", b)
	}
}

func TestNewBackendFromEnv_Direct(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "direct")

	b := NewBackendFromEnv()
	if _, ok := b.(*DirectBackend); !ok {
		t.Fatalf("NewBackendFromEnv() returned %T, want *DirectBackend", b)
	}
}

func TestNewBackendFromEnv_Unset(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "")

	b := NewBackendFromEnv()
	if _, ok := b.(*DirectBackend); !ok {
		t.Fatalf("NewBackendFromEnv() with empty env returned %T, want *DirectBackend", b)
	}
}

func TestNewBackendFromEnv_Sandlock(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "sandlock")

	b := NewBackendFromEnv()
	// Should be sandlock on Linux, direct on other platforms
	if b.Name() != "sandlock" && b.Name() != "direct" {
		t.Fatalf("NewBackendFromEnv() returned %q, want sandlock or direct", b.Name())
	}
}

func TestNewBackendFromEnv_Wasm(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "wasm")

	b := NewBackendFromEnv()
	// Should be wasm if wasmtime available, direct otherwise
	if b.Name() != "wasm" && b.Name() != "direct" {
		t.Fatalf("NewBackendFromEnv() returned %q, want wasm or direct", b.Name())
	}
}

// --- DirectBackend extended tests ---

func TestDirectBackend_WrapCommandIgnoresConfig(t *testing.T) {
	t.Parallel()

	backend := &DirectBackend{}
	cfg := Config{
		MaxMemoryMB:  1024,
		CPUTimeSecs:  30,
		AllowNetwork: false,
		WorkDir:      "/some/dir",
		EnvVars:      []string{"FOO=bar"},
	}

	cmd, err := backend.WrapCommand(context.Background(), "echo", []string{"hello"}, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	// Direct backend should not add any sandbox flags
	if len(cmd.Args) != 2 || cmd.Args[0] != "echo" || cmd.Args[1] != "hello" {
		t.Fatalf("cmd.Args = %v, want [echo hello]", cmd.Args)
	}
}

func TestDirectBackend_WrapCommandPreservesContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := &DirectBackend{}
	cmd, err := backend.WrapCommand(ctx, "sleep", []string{"10"}, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	// cmd should not be nil (we just test it was created with context)
	if cmd == nil {
		t.Fatal("WrapCommand() returned nil cmd")
	}
}

func TestDirectBackend_WrapCommandMultipleArgs(t *testing.T) {
	t.Parallel()

	backend := &DirectBackend{}
	args := []string{"-la", "--color=auto", "/tmp"}
	cmd, err := backend.WrapCommand(context.Background(), "ls", args, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	want := append([]string{"ls"}, args...)
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("cmd.Args = %v, want %v", cmd.Args, want)
	}
}

// --- Separator / argument injection checks ---

func TestSandlockBackend_CommandSeparator(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cmd, err := backend.WrapCommand(context.Background(), "echo", []string{"hello"}, Config{AllowNetwork: true})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	// Verify -- separator exists before the command
	separatorIdx := -1
	for i, a := range cmd.Args {
		if a == "--" {
			separatorIdx = i
			break
		}
	}
	if separatorIdx < 0 {
		t.Fatalf("missing -- separator in args: %v", cmd.Args)
	}
	if separatorIdx >= len(cmd.Args)-1 || cmd.Args[separatorIdx+1] != "echo" {
		t.Fatalf("command should immediately follow --, args: %v", cmd.Args)
	}
}

func TestSandlockBackend_WrapCommandArgsWithSpecialChars(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cfg := Config{AllowNetwork: true}

	// Test that special characters in args are passed through
	cmd, err := backend.WrapCommand(context.Background(), "sh", []string{"-c", "echo 'hello world' && exit 0"}, cfg)
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	// The args after -- should be exactly what we passed
	separatorIdx := -1
	for i, a := range cmd.Args {
		if a == "--" {
			separatorIdx = i
			break
		}
	}
	if separatorIdx < 0 {
		t.Fatal("missing -- separator")
	}
	trailing := cmd.Args[separatorIdx+1:]
	wantTrailing := []string{"sh", "-c", "echo 'hello world' && exit 0"}
	if !reflect.DeepEqual(trailing, wantTrailing) {
		t.Fatalf("args after -- = %v, want %v", trailing, wantTrailing)
	}
}

func TestWasmBackend_CommandSeparator(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	program := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(program, []byte("wasm"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	bin := writeExecutable(t, "wasmtime")
	backend := &WasmBackend{wasmtimePath: bin}
	cmd, err := backend.WrapCommand(context.Background(), program, []string{"arg1"}, Config{WorkDir: dir})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}

	separatorIdx := -1
	for i, a := range cmd.Args {
		if a == "--" {
			separatorIdx = i
			break
		}
	}
	if separatorIdx < 0 {
		t.Fatalf("missing -- separator in args: %v", cmd.Args)
	}
	if cmd.Args[separatorIdx+1] != program {
		t.Fatalf("program should immediately follow --, args: %v", cmd.Args)
	}
}

// --- Name tests for all backends ---

func TestAllBackendsName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		backend SandboxBackend
		want    string
	}{
		{&DirectBackend{}, "direct"},
		{&SandlockBackend{}, "sandlock"},
		{&WasmBackend{}, "wasm"},
	}
	for _, tc := range tests {
		if got := tc.backend.Name(); got != tc.want {
			t.Errorf("%T.Name() = %q, want %q", tc.backend, got, tc.want)
		}
	}
}

// --- Ensure strings import is used ---

func TestSandlockBackend_WrapCommandNoNetworkFlagContent(t *testing.T) {
	t.Parallel()

	bin := writeExecutable(t, "sandlock")
	backend := &SandlockBackend{binaryPath: bin}
	cmd, err := backend.WrapCommand(context.Background(), "test", nil, Config{})
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	argsStr := strings.Join(cmd.Args, " ")
	if !strings.Contains(argsStr, "--no-network") {
		t.Fatalf("expected --no-network in args: %s", argsStr)
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

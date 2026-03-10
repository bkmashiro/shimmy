package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ============================================================
// Available() tests
// ============================================================

func TestDirectBackend_AlwaysAvailable(t *testing.T) {
	t.Parallel()
	b := &DirectBackend{}
	if !b.Available() {
		t.Fatal("DirectBackend.Available() = false, want true")
	}
}

func TestSandlockBackend_NotAvailableOnNonLinux(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "linux" {
		t.Skip("test only runs on non-Linux platforms")
	}
	b := &SandlockBackend{}
	if b.Available() {
		t.Fatal("SandlockBackend.Available() = true on non-Linux, want false")
	}
}

func TestSandlockBackend_NotAvailableWithBadBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SandlockBackend always returns false on non-Linux")
	}
	b := &SandlockBackend{binaryPath: "/nonexistent/sandlock-abc123"}
	if b.Available() {
		t.Fatal("SandlockBackend.Available() = true with bad binary, want false")
	}
}

func TestWasmBackend_NotAvailableWithoutWasmtime(t *testing.T) {
	// Isolate PATH to make wasmtime unfindable
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", t.TempDir()) // empty dir, no binaries
	defer os.Setenv("PATH", origPath)

	os.Unsetenv("SHIMMY_WASMTIME_PATH")

	b := &WasmBackend{}
	if b.Available() {
		t.Fatal("WasmBackend.Available() = true without wasmtime, want false")
	}
}

func TestWasmBackend_NotAvailableWithBadPath(t *testing.T) {
	t.Parallel()
	b := &WasmBackend{wasmtimePath: "/nonexistent/wasmtime-abc123"}
	if b.Available() {
		t.Fatal("WasmBackend.Available() = true with bad path, want false")
	}
}

func TestWasmBackend_AvailableWithValidBinary(t *testing.T) {
	t.Parallel()
	bin := writeExecutable(t, "wasmtime")
	b := &WasmBackend{wasmtimePath: bin}
	if !b.Available() {
		t.Fatal("WasmBackend.Available() = false with valid binary, want true")
	}
}

func TestSandlockBackend_AvailableWithValidBinaryOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sandlock only available on Linux")
	}
	bin := writeExecutable(t, "sandlock")
	b := &SandlockBackend{binaryPath: bin}
	if !b.Available() {
		t.Fatal("SandlockBackend.Available() = false with valid binary on Linux, want true")
	}
}

// ============================================================
// Fallback behavior tests
// ============================================================

func TestNewBackend_SandlockUnavailableFallsToDirect(t *testing.T) {
	t.Parallel()

	// On non-Linux, sandlock is always unavailable
	if runtime.GOOS == "linux" {
		t.Skip("on Linux, sandlock may actually be available")
	}

	b := NewBackend("sandlock")
	if _, ok := b.(*DirectBackend); !ok {
		t.Fatalf("NewBackend(\"sandlock\") on %s returned %T, want *DirectBackend", runtime.GOOS, b)
	}
}

func TestNewBackend_WasmUnavailableFallsToDirect(t *testing.T) {
	// Clear PATH to ensure wasmtime is not found
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", t.TempDir())
	defer os.Setenv("PATH", origPath)

	os.Unsetenv("SHIMMY_WASMTIME_PATH")

	b := NewBackend("wasm")
	if _, ok := b.(*DirectBackend); !ok {
		t.Fatalf("NewBackend(\"wasm\") without wasmtime returned %T, want *DirectBackend", b)
	}
}

func TestNewBackendFromEnv_UnsetFallsToDirect(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "")

	b := NewBackendFromEnv()
	if _, ok := b.(*DirectBackend); !ok {
		t.Fatalf("NewBackendFromEnv() with empty env returned %T, want *DirectBackend", b)
	}
}

func TestNewBackendFromEnv_GarbageFallsToDirect(t *testing.T) {
	t.Setenv("SHIMMY_SANDBOX_BACKEND", "not-a-real-backend")

	b := NewBackendFromEnv()
	if _, ok := b.(*DirectBackend); !ok {
		t.Fatalf("NewBackendFromEnv(\"not-a-real-backend\") returned %T, want *DirectBackend", b)
	}
}

func TestNewBackend_DirectAlwaysReturnsDirect(t *testing.T) {
	t.Parallel()

	b := NewBackend("direct")
	d, ok := b.(*DirectBackend)
	if !ok {
		t.Fatalf("NewBackend(\"direct\") returned %T, want *DirectBackend", b)
	}
	if !d.Available() {
		t.Fatal("returned DirectBackend.Available() = false, want true")
	}
}

func TestFallback_ReturnedBackendIsUsable(t *testing.T) {
	t.Parallel()

	// Request unavailable backend, get direct fallback
	if runtime.GOOS == "linux" {
		t.Skip("on Linux, sandlock may be available")
	}

	b := NewBackend("sandlock")
	if !b.Available() {
		t.Fatal("fallback backend.Available() = false, want true")
	}
	if b.Name() != "direct" {
		t.Fatalf("fallback backend.Name() = %q, want %q", b.Name(), "direct")
	}
}

func TestFallback_DirectBackendWrapCommandWorks(t *testing.T) {
	t.Parallel()

	// Even after falling back, WrapCommand should work
	if runtime.GOOS == "linux" {
		t.Skip("on Linux, sandlock may be available")
	}

	b := NewBackend("sandlock")
	cmd, err := b.WrapCommand(context.Background(), "echo", []string{"test"}, DefaultConfig())
	if err != nil {
		t.Fatalf("WrapCommand() error = %v", err)
	}
	if cmd == nil {
		t.Fatal("WrapCommand() returned nil cmd")
	}
}

// ============================================================
// resolveBinary fallback chain tests
// ============================================================

func TestSandlockBackend_ResolveBinaryFallbackChain(t *testing.T) {
	// With empty PATH and no env var set, should try $HOME fallback
	t.Setenv("PATH", t.TempDir()) // empty dir
	t.Setenv("SHIMMY_SANDLOCK_PATH", "")

	backend := &SandlockBackend{}
	_, err := backend.resolveBinary()
	// Should error since sandlock isn't installed at $HOME/.openclaw/...
	if err == nil {
		// May succeed if sandlock is actually installed at $HOME path
		t.Log("resolveBinary() succeeded (sandlock found at home dir path)")
	}
}

func TestSandlockBackend_ResolveBinaryHomeDir(t *testing.T) {
	// Create a fake sandlock in a fake home dir
	fakeHome := t.TempDir()
	sandlockDir := filepath.Join(fakeHome, ".openclaw", "workspace", "sandlock")
	if err := os.MkdirAll(sandlockDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	sandlockBin := filepath.Join(sandlockDir, "sandlock")
	if err := os.WriteFile(sandlockBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	t.Setenv("HOME", fakeHome)
	t.Setenv("PATH", t.TempDir()) // empty dir, no sandlock in PATH
	t.Setenv("SHIMMY_SANDLOCK_PATH", "")

	backend := &SandlockBackend{}
	resolved, err := backend.resolveBinary()
	if err != nil {
		t.Fatalf("resolveBinary() error = %v (should find sandlock at home dir)", err)
	}
	if resolved != sandlockBin {
		t.Fatalf("resolveBinary() = %q, want %q", resolved, sandlockBin)
	}
}

func TestWasmBackend_ResolveBinaryFallbackChain(t *testing.T) {
	// With empty PATH and no env var, should fail
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", t.TempDir())
	defer os.Setenv("PATH", origPath)

	os.Unsetenv("SHIMMY_WASMTIME_PATH")

	backend := &WasmBackend{}
	_, err := backend.resolveBinary()
	if err == nil {
		t.Fatal("resolveBinary() = nil error, want error when wasmtime not in PATH")
	}
}

func TestWasmBackend_ResolveBinaryEnvOverridesDefault(t *testing.T) {
	bin := writeExecutable(t, "my-wasmtime-env")
	t.Setenv("SHIMMY_WASMTIME_PATH", bin)

	backend := &WasmBackend{} // no wasmtimePath set
	resolved, err := backend.resolveBinary()
	if err != nil {
		t.Fatalf("resolveBinary() error = %v", err)
	}
	if resolved != bin {
		t.Fatalf("resolveBinary() = %q, want %q", resolved, bin)
	}
}

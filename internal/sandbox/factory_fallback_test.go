package sandbox

import "testing"

func TestNewBackend_EmptyNameFallsBackToDirectWhenSandlockAndWasmtimeUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv(sandlockPathEnv, "")
	t.Setenv(wasmtimePathEnv, "")
	t.Setenv("HOME", t.TempDir())

	backend := NewBackend("")
	if _, ok := backend.(*DirectBackend); !ok {
		t.Fatalf("NewBackend(\"\") returned %T, want *DirectBackend", backend)
	}
	if !backend.Available() {
		t.Fatal("fallback backend.Available() = false, want true")
	}
	if backend.Name() != "direct" {
		t.Fatalf("fallback backend.Name() = %q, want %q", backend.Name(), "direct")
	}
}

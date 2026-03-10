package sandbox

import "os"

// NewBackend returns a requested backend or falls back to direct execution.
// When name is empty, it auto-detects the best available backend
// (sandlock → wasm → direct).
func NewBackend(name string) SandboxBackend {
	switch name {
	case "sandlock":
		backend := &SandlockBackend{}
		if backend.Available() {
			return backend
		}
	case "wasm":
		backend := &WasmBackend{}
		if backend.Available() {
			return backend
		}
	case "direct":
		return &DirectBackend{}
	case "":
		// Auto-detect: prefer sandlock, then wasm, then direct
		if b := (&SandlockBackend{}); b.Available() {
			return b
		}
		if b := (&WasmBackend{}); b.Available() {
			return b
		}
		return &DirectBackend{}
	}

	return &DirectBackend{}
}

// NewBackendFromEnv reads SHIMMY_SANDBOX_BACKEND and returns the matching backend.
func NewBackendFromEnv() SandboxBackend {
	return NewBackend(os.Getenv("SHIMMY_SANDBOX_BACKEND"))
}

package sandbox

import "os"

// NewBackend returns a requested backend or falls back to direct execution.
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
	}

	return &DirectBackend{}
}

// NewBackendFromEnv reads SHIMMY_SANDBOX_BACKEND and returns the matching backend.
func NewBackendFromEnv() SandboxBackend {
	return NewBackend(os.Getenv("SHIMMY_SANDBOX_BACKEND"))
}

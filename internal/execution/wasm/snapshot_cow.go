package wasm

import "errors"

var (
	// ErrCowUnavailable means the target platform or allocator cannot provide
	// the stable file-backed mapping required by the COW strategy.
	ErrCowUnavailable = errors.New("cow: prepared-memory COW is unavailable")
	// ErrCowBaselineMismatch means an independently prepared instance did not
	// produce the same linear-memory image as the dispatcher canonical image.
	ErrCowBaselineMismatch = errors.New("cow: prepared baseline does not match the canonical image")
	// ErrCowMemoryFreed means an operation targeted allocator-owned memory after
	// wazero released it through experimental.LinearMemory.Free.
	ErrCowMemoryFreed = errors.New("cow: linear memory has been freed")
	// ErrCowImageClosed means reset was attempted after the dispatcher-scoped
	// canonical image had been closed.
	ErrCowImageClosed = errors.New("cow: canonical image is closed")
	// ErrCowNotAttached means reset was requested before baseline publication or
	// attachment completed.
	ErrCowNotAttached = errors.New("cow: linear memory is not attached to an image")
	// ErrCowMemoryDrifted means the live pointer or visible size no longer
	// matches the fixed prepared image.
	ErrCowMemoryDrifted = errors.New("cow: linear memory pointer or size drifted")
)

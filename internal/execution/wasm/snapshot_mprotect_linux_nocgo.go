//go:build linux && !cgo

package wasm

import (
	"fmt"

	"github.com/tetratelabs/wazero/api"
)

// MprotectStrategy is a placeholder type for builds without CGo.
// The real implementation lives in snapshot_mprotect_linux.go (requires CGo).
type MprotectStrategy struct{}

// NewMprotectStrategy returns an error on non-CGo builds. The mprotect dirty-
// page tracking strategy requires CGo for the C SIGSEGV signal handler.
func NewMprotectStrategy(_ api.Memory) (*MprotectStrategy, error) {
	return nil, fmt.Errorf("mprotect strategy requires CGo (build with CGO_ENABLED=1)")
}

// Take implements SnapshotStrategy (stub).
func (s *MprotectStrategy) Take(_ api.Memory) error { return nil }

// Restore implements SnapshotStrategy (stub).
func (s *MprotectStrategy) Restore(_ api.Memory) error { return nil }

// Close implements SnapshotStrategy (stub).
func (s *MprotectStrategy) Close() error { return nil }

// forceDirtyNPages is a no-op stub for non-CGo builds (benchmark use only).
func (s *MprotectStrategy) forceDirtyNPages(_ int) {}

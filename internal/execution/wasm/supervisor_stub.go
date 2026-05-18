//go:build !linux

package wasm

import (
	"github.com/tetratelabs/wazero/api"
)

// selectStrategy returns FullMemcpyStrategy on non-Linux platforms where
// userfaultfd, soft-dirty, and mprotect strategies are not available.
func (s *wasmSupervisor) selectStrategy(mem api.Memory) SnapshotStrategy {
	return NewFullMemcpyStrategy()
}

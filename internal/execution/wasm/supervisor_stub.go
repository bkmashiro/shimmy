//go:build !linux

package wasm

import (
	"github.com/tetratelabs/wazero/api"
)

// selectStrategy returns FullMemcpyStrategy on non-Linux platforms where
// userfaultfd is not available.
func (s *wasmSupervisor) selectStrategy(mem api.Memory) SnapshotStrategy {
	return NewFullMemcpyStrategy()
}

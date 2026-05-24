//go:build !linux

package wasm

import (
	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// selectSnapshotStrategy returns FullMemcpyStrategy on non-Linux platforms
// where userfaultfd, soft-dirty, and mprotect strategies are not available.
func selectSnapshotStrategy(_ string, _ api.Memory, _ *zap.Logger) SnapshotStrategy {
	return NewFullMemcpyStrategy()
}

// selectStrategy delegates to the package-level selectSnapshotStrategy.
func (s *wasmSupervisor) selectStrategy(mem api.Memory) SnapshotStrategy {
	return selectSnapshotStrategy(s.snapshotMode, mem, s.log)
}

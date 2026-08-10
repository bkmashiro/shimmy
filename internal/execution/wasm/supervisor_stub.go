//go:build !linux

package wasm

import (
	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// selectSnapshotStrategy returns FullMemcpyStrategy on non-Linux platforms
// where userfaultfd, soft-dirty, mprotect, and COW strategies are unavailable.
func selectSnapshotStrategy(mode string, _ api.Memory, log *zap.Logger) SnapshotStrategy {
	if mode == "" || mode == "memcpy" {
		logSnapshotSelection(log, mode, "memcpy", "")
	} else {
		logSnapshotSelection(log, mode, "memcpy", "snapshot strategy is unavailable on non-Linux")
	}
	return NewFullMemcpyStrategy()
}

// selectStrategy delegates to the package-level selectSnapshotStrategy.
func (s *wasmSupervisor) selectStrategy(mem api.Memory) SnapshotStrategy {
	return selectSnapshotStrategy(s.snapshotMode, mem, s.log)
}

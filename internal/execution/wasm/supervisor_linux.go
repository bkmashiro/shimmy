//go:build linux

package wasm

import (
	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// selectStrategy selects the SnapshotStrategy to use for this supervisor.
// On Linux, if useUffd is true it attempts to create a UffdStrategy backed by
// the WASM linear memory. If uffd is unavailable (seccomp, missing kernel
// support, etc.) it logs a warning and falls back to FullMemcpyStrategy.
func (s *wasmSupervisor) selectStrategy(mem api.Memory) SnapshotStrategy {
	if s.useUffd && mem != nil {
		us, err := NewUffdStrategy(mem)
		if err != nil {
			s.log.Warn("uffd unavailable, falling back to full memcpy",
				zap.Error(err))
		} else {
			s.log.Info("using uffd dirty-page tracking strategy",
				zap.Uint32("mem_size", mem.Size()))
			return us
		}
	}
	return NewFullMemcpyStrategy()
}

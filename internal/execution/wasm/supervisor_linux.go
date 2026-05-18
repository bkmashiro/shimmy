//go:build linux

package wasm

import (
	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// selectStrategy selects the SnapshotStrategy to use for this supervisor.
//
// The snapshotMode field (from Config.SnapshotMode) takes precedence over the
// legacy useUffd bool. Valid modes:
//
//	"memcpy"     — FullMemcpyStrategy (default, always available)
//	"soft-dirty" — SoftDirtyStrategy via /proc/self/pagemap
//	"mprotect"   — MprotectStrategy via mprotect(PROT_READ) + SIGSEGV
//	"uffd"       — UffdStrategy via userfaultfd write-protect
//
// Falls back to FullMemcpyStrategy if the requested strategy is unavailable.
// For backward compatibility, useUffd=true is treated as snapshotMode="uffd"
// when snapshotMode is empty.
func (s *wasmSupervisor) selectStrategy(mem api.Memory) SnapshotStrategy {
	mode := s.snapshotMode
	if mode == "" && s.useUffd {
		mode = "uffd"
	}

	switch mode {
	case "soft-dirty":
		sd, err := NewSoftDirtyStrategy(mem)
		if err != nil {
			s.log.Warn("soft-dirty unavailable, falling back to full memcpy",
				zap.Error(err))
			return NewFullMemcpyStrategy()
		}
		s.log.Info("using soft-dirty page tracking strategy",
			zap.Uint32("mem_size", mem.Size()))
		return sd

	case "mprotect":
		mp, err := NewMprotectStrategy(mem)
		if err != nil {
			s.log.Warn("mprotect unavailable, falling back to full memcpy",
				zap.Error(err))
			return NewFullMemcpyStrategy()
		}
		s.log.Info("using mprotect dirty-page tracking strategy",
			zap.Uint32("mem_size", mem.Size()))
		return mp

	case "uffd":
		if mem == nil {
			return NewFullMemcpyStrategy()
		}
		us, err := NewUffdStrategy(mem)
		if err != nil {
			s.log.Warn("uffd unavailable, falling back to full memcpy",
				zap.Error(err))
			return NewFullMemcpyStrategy()
		}
		s.log.Info("using uffd dirty-page tracking strategy",
			zap.Uint32("mem_size", mem.Size()))
		return us

	default:
		// "memcpy" or empty — always-available baseline.
		return NewFullMemcpyStrategy()
	}
}

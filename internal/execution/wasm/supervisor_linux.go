//go:build linux

package wasm

import (
	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// selectSnapshotStrategy creates the SnapshotStrategy for the given mode and
// memory. If the requested strategy is unavailable it falls back to
// FullMemcpyStrategy and logs a warning. This is the single factory used by
// both wasmSupervisor and ReactorPythonRunner.
//
// Valid modes:
//
//	"memcpy"     — FullMemcpyStrategy (default, always available)
//	"soft-dirty" — SoftDirtyStrategy via /proc/self/pagemap
//	"mprotect"   — MprotectStrategy via mprotect(PROT_READ) + SIGSEGV
//	"uffd"       — UffdStrategy via userfaultfd write-protect
func selectSnapshotStrategy(mode string, mem api.Memory, log *zap.Logger) SnapshotStrategy {
	switch mode {
	case "soft-dirty":
		sd, err := NewSoftDirtyStrategy(mem)
		if err != nil {
			log.Warn("soft-dirty unavailable, falling back to full memcpy",
				zap.Error(err))
			return NewFullMemcpyStrategy()
		}
		log.Info("using soft-dirty page tracking strategy",
			zap.Uint32("mem_size", mem.Size()))
		return sd

	case "mprotect":
		mp, err := NewMprotectStrategy(mem)
		if err != nil {
			log.Warn("mprotect unavailable, falling back to full memcpy",
				zap.Error(err))
			return NewFullMemcpyStrategy()
		}
		log.Info("using mprotect dirty-page tracking strategy",
			zap.Uint32("mem_size", mem.Size()))
		return mp

	case "uffd":
		if mem == nil {
			return NewFullMemcpyStrategy()
		}
		us, err := NewUffdStrategy(mem)
		if err != nil {
			log.Warn("uffd unavailable, falling back to full memcpy",
				zap.Error(err))
			return NewFullMemcpyStrategy()
		}
		log.Info("using uffd dirty-page tracking strategy",
			zap.Uint32("mem_size", mem.Size()))
		return us

	default:
		// "memcpy" or empty — always-available baseline.
		return NewFullMemcpyStrategy()
	}
}

// selectStrategy delegates to the package-level selectSnapshotStrategy.
func (s *wasmSupervisor) selectStrategy(mem api.Memory) SnapshotStrategy {
	return selectSnapshotStrategy(s.snapshotMode, mem, s.log)
}

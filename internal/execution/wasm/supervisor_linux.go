//go:build linux

package wasm

import (
	"os"

	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// mprotectOptInEnv is the env var an operator must set to opt in to the
// experimental mprotect snapshot strategy. The mprotect strategy installs a
// process-wide SIGSEGV handler from CGo that intercepts WASM linear-memory
// write faults. Any nil-pointer panic in unrelated Go code lands in the same
// handler and is chained back to the Go runtime; that chaining works in our
// tests but is fragile in production (interaction with libraries that also
// install SIGSEGV handlers, Sentry crash reporting, Go runtime upgrades, etc.)
// so the strategy is gated behind an explicit opt-in to prevent accidental
// selection by an operator who only set FUNCTION_WASM_SNAPSHOT_MODE=mprotect.
const mprotectOptInEnv = "FUNCTION_WASM_ALLOW_EXPERIMENTAL_MPROTECT"

// mprotectOptedIn reports whether the operator has explicitly opted in to the
// experimental mprotect snapshot strategy.
func mprotectOptedIn() bool {
	v := os.Getenv(mprotectOptInEnv)
	return v == "true" || v == "1"
}

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
//	               (EXPERIMENTAL — requires FUNCTION_WASM_ALLOW_EXPERIMENTAL_MPROTECT=true)
//	"uffd"       — UffdStrategy via userfaultfd write-protect
func selectSnapshotStrategy(mode string, mem api.Memory, log *zap.Logger) SnapshotStrategy {
	switch mode {
	case "soft-dirty":
		sd, err := NewSoftDirtyStrategy(mem)
		if err != nil {
			logSnapshotSelection(log, mode, "memcpy", err.Error())
			return NewFullMemcpyStrategy()
		}
		logSnapshotSelection(log, mode, "soft-dirty", "")
		return sd

	case "mprotect":
		if !mprotectOptedIn() {
			logSnapshotSelection(
				log,
				mode,
				"memcpy",
				"experimental mprotect requires "+mprotectOptInEnv+"=true",
			)
			return NewFullMemcpyStrategy()
		}
		mp, err := NewMprotectStrategy(mem)
		if err != nil {
			logSnapshotSelection(log, mode, "memcpy", err.Error())
			return NewFullMemcpyStrategy()
		}
		logSnapshotSelection(log, mode, "mprotect", "")
		log.Warn("using EXPERIMENTAL mprotect dirty-page tracking strategy — process-wide SIGSEGV handler installed",
			zap.Uint32("mem_size", mem.Size()))
		return mp

	case "uffd":
		if mem == nil {
			logSnapshotSelection(log, mode, "memcpy", "WASM memory is unavailable")
			return NewFullMemcpyStrategy()
		}
		us, err := NewUffdStrategy(mem)
		if err != nil {
			logSnapshotSelection(log, mode, "memcpy", err.Error())
			return NewFullMemcpyStrategy()
		}
		logSnapshotSelection(log, mode, "uffd", "")
		return us

	default:
		if mode == "cow" {
			logSnapshotSelection(log, mode, "memcpy", "COW runtime support is unavailable")
			return NewFullMemcpyStrategy()
		}
		// "memcpy" or empty — always-available baseline. Invalid modes are
		// rejected by Config.validateSnapshotMode before strategy selection.
		logSnapshotSelection(log, mode, "memcpy", "")
		return NewFullMemcpyStrategy()
	}
}

// selectStrategy delegates to the package-level selectSnapshotStrategy.
func (s *wasmSupervisor) selectStrategy(mem api.Memory) SnapshotStrategy {
	return selectSnapshotStrategy(s.snapshotMode, mem, s.log)
}

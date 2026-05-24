package wasm

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// wasmSupervisor manages a single instantiated WASM module. After the module
// is initialised its linear memory is snapshotted; the snapshot is restored
// after every Send so that the next request sees a clean initial state. This
// gives cheap warm-start semantics without re-compiling the module.
type wasmSupervisor struct {
	mu sync.Mutex

	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	modCfg   wazero.ModuleConfig

	mod     api.Module
	adapter *wasmAdapter

	// strategy implements the snapshot/restore mechanism. The default is
	// FullMemcpyStrategy; on Linux with the appropriate kernel features,
	// UffdStrategy, MprotectStrategy, or SoftDirtyStrategy may be used.
	strategy SnapshotStrategy

	// snapshotMode selects the snapshot strategy. See Config.SnapshotMode for
	// valid values. Resolved from Config.UseUffd by Config.applyDefaults().
	snapshotMode string

	// healthy is true when the supervisor is in a known-good state and can be
	// safely returned to the pool. It is set to false when restoreSnapshot fails,
	// indicating the WASM module's memory state is undefined.
	healthy bool

	timeout time.Duration
	log     *zap.Logger
}

func newWasmSupervisor(
	rt wazero.Runtime,
	compiled wazero.CompiledModule,
	modCfg wazero.ModuleConfig,
	timeout time.Duration,
	snapshotMode string,
	log *zap.Logger,
) *wasmSupervisor {
	return &wasmSupervisor{
		runtime:      rt,
		compiled:     compiled,
		modCfg:       modCfg,
		snapshotMode: snapshotMode,
		timeout:      timeout,
		log:          log.Named("supervisor_wasm"),
	}
}

// Start instantiates the compiled module, runs any WASI start function, then
// snapshots linear memory.
func (s *wasmSupervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.mod != nil {
		return nil
	}

	s.log.Debug("instantiating wasm module")

	// Apply start functions on top of the provided (sandboxed) module config.
	instCfg := s.modCfg.WithStartFunctions("_initialize", "_start")

	mod, err := s.runtime.InstantiateModule(ctx, s.compiled, instCfg)
	if err != nil {
		return fmt.Errorf("wasm: instantiate module: %w", err)
	}

	s.mod = mod
	s.adapter = newWasmAdapter(mod, s.log)
	s.healthy = true

	// Select snapshot strategy now that memory is available.
	s.strategy = s.selectStrategy(mod.Memory())

	// Snapshot linear memory so we can restore it before each request.
	if err := s.takeSnapshot(); err != nil {
		_ = s.strategy.Close()
		_ = mod.Close(ctx)
		s.mod = nil
		return fmt.Errorf("wasm: snapshot memory: %w", err)
	}

	memSize := uint32(0)
	if m := s.mod.Memory(); m != nil {
		memSize = m.Size()
	}
	s.log.Debug("wasm module ready",
		zap.Uint32("snapshot_bytes", memSize),
		zap.String("strategy", fmt.Sprintf("%T", s.strategy)),
	)

	return nil
}

// Send calls the guest's evaluate function, then restores linear memory from
// the snapshot so the next request starts from a clean state.
func (s *wasmSupervisor) Send(
	ctx context.Context,
	method string,
	data map[string]any,
) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.mod == nil || s.adapter == nil {
		return nil, fmt.Errorf("wasm: supervisor not started")
	}

	result, err := s.adapter.send(ctx, method, data, s.timeout)

	// Restore memory snapshot to keep state clean for the next request.
	// If restore fails, mark the supervisor unhealthy so the dispatcher
	// discards it rather than returning it to the pool with undefined state.
	if restoreErr := s.restoreSnapshot(); restoreErr != nil {
		s.log.Error("failed to restore memory snapshot — marking supervisor unhealthy", zap.Error(restoreErr))
		s.healthy = false
		if err == nil {
			err = fmt.Errorf("wasm: restore snapshot: %w", restoreErr)
		}
	}

	return result, err
}

// IsHealthy reports whether the supervisor is in a known-good state.
// Safe to call without holding s.mu (acquires the lock internally). (I-3 fix)
func (s *wasmSupervisor) IsHealthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.healthy
}

// Shutdown closes the module instance and releases resources.
func (s *wasmSupervisor) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.mod == nil {
		return nil
	}

	s.log.Debug("shutting down wasm module instance")

	if err := s.mod.Close(ctx); err != nil {
		return fmt.Errorf("wasm: close module: %w", err)
	}

	s.mod = nil
	s.adapter = nil

	if err := s.strategy.Close(); err != nil {
		s.log.Warn("failed to close snapshot strategy", zap.Error(err))
	}

	return nil
}

// takeSnapshot captures the guest's linear memory via the active strategy.
// Must be called with s.mu held.
func (s *wasmSupervisor) takeSnapshot() error {
	mem := s.mod.Memory()
	if mem == nil {
		return nil
	}
	return s.strategy.Take(mem)
}

// restoreSnapshot restores the guest's linear memory from the last snapshot
// via the active strategy. Must be called with s.mu held.
func (s *wasmSupervisor) restoreSnapshot() error {
	if s.mod == nil {
		return nil
	}
	mem := s.mod.Memory()
	if mem == nil {
		return nil
	}
	return s.strategy.Restore(mem)
}

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
	// FullMemcpyStrategy; on Linux with userfaultfd available a probe strategy
	// is used instead.
	strategy SnapshotStrategy

	timeout time.Duration
	log     *zap.Logger
}

func newWasmSupervisor(
	rt wazero.Runtime,
	compiled wazero.CompiledModule,
	modCfg wazero.ModuleConfig,
	timeout time.Duration,
	log *zap.Logger,
) *wasmSupervisor {
	return &wasmSupervisor{
		runtime:  rt,
		compiled: compiled,
		modCfg:   modCfg,
		strategy: NewSnapshotStrategy(),
		timeout:  timeout,
		log:      log.Named("supervisor_wasm"),
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

	// Snapshot linear memory so we can restore it before each request.
	if err := s.takeSnapshot(); err != nil {
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

	// Always restore memory snapshot, even on error, to keep state clean for
	// the next request.
	if restoreErr := s.restoreSnapshot(); restoreErr != nil {
		s.log.Error("failed to restore memory snapshot", zap.Error(restoreErr))
	}

	return result, err
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

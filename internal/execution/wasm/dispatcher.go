package wasm

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/dispatcher"
)

// Dispatcher implements [dispatcher.Dispatcher] for the WASM execution
// backend. It compiles the .wasm module once at startup, then maintains a pool
// of pre-initialised [wasmSupervisor] instances (one compiled module, N module
// instances). Requests are dispatched by acquiring a supervisor from the pool,
// calling its Send, and returning it to the pool.
type Dispatcher struct {
	cfg      Config
	rt       wazero.Runtime
	compiled wazero.CompiledModule
	modCfg   wazero.ModuleConfig
	pool     chan *wasmSupervisor
	log      *zap.Logger
}

var _ dispatcher.Dispatcher = (*Dispatcher)(nil)

// NewDispatcher creates a new WASM dispatcher. Compilation and pool
// initialisation happen in Start.
func NewDispatcher(cfg Config, log *zap.Logger) *Dispatcher {
	return &Dispatcher{
		cfg: cfg,
		log: log.Named("dispatcher_wasm"),
	}
}

// Start reads and compiles the .wasm file, sets up WASI host functions, and
// pre-warms the supervisor pool.
func (d *Dispatcher) Start(ctx context.Context) error {
	// Pick up sandbox overrides from FUNCTION_WASM_* env vars (including
	// FUNCTION_WASM_MODULE as an alternative to FUNCTION_COMMAND), then apply
	// sensible defaults for any fields still at their zero values.
	d.cfg.applyEnv()
	d.cfg.applyDefaults()

	if d.cfg.ModulePath == "" {
		return fmt.Errorf("wasm: ModulePath must be set (FUNCTION_COMMAND or FUNCTION_WASM_MODULE)")
	}

	maxInstances := d.cfg.MaxInstances
	if maxInstances <= 0 {
		maxInstances = runtime.NumCPU()
	}

	d.log.Info("starting wasm dispatcher",
		zap.String("module", d.cfg.ModulePath),
		zap.Int("max_instances", maxInstances),
		zap.Uint32("max_memory_pages", d.cfg.MaxMemoryPages),
		zap.Duration("timeout", d.cfg.Timeout),
	)

	// Read the .wasm bytes from disk.
	wasmBytes, err := os.ReadFile(d.cfg.ModulePath)
	if err != nil {
		return fmt.Errorf("wasm: read module file %q: %w", d.cfg.ModulePath, err)
	}

	// Build the runtime config with memory limit and context-done interruption.
	rtCfg := wazero.NewRuntimeConfig().
		// WithCloseOnContextDone causes wazero to interrupt a running WASM module
		// when the call context is cancelled or times out, preventing goroutine leaks.
		WithCloseOnContextDone(true)
	if d.cfg.MaxMemoryPages > 0 {
		rtCfg = rtCfg.WithMemoryLimitPages(d.cfg.MaxMemoryPages)
	}

	// Wire in on-disk compilation cache when configured.
	if d.cfg.CompileCacheDir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(d.cfg.CompileCacheDir)
		if err != nil {
			d.log.Warn("failed to create wazero compilation cache, continuing without cache",
				zap.String("dir", d.cfg.CompileCacheDir),
				zap.Error(err))
		} else {
			rtCfg = rtCfg.WithCompilationCache(cache)
			d.log.Info("wazero compilation cache enabled", zap.String("dir", d.cfg.CompileCacheDir))
		}
	}

	// Create a single wazero runtime shared by all instances.
	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)
	d.rt = rt

	// Instantiate WASI host functions. Most evaluation functions will need at
	// least minimal WASI support (e.g. for memory allocation helpers compiled
	// from C/Rust/TinyGo).
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return fmt.Errorf("wasm: instantiate wasi: %w", err)
	}

	// Compile the module once; all instances share the compiled code.
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx)
		return fmt.Errorf("wasm: compile module: %w", err)
	}
	d.compiled = compiled

	// Build a locked-down ModuleConfig: no filesystem, no env vars, no
	// stdin/stdout/stderr, no args. Only allow nanosleep and wall/mono clocks
	// which the Go runtime needs.
	modCfg := wazero.NewModuleConfig().
		WithName("").
		WithSysNanosleep().
		WithSysWalltime().
		WithSysNanotime()

	// Filesystem: mount allowed paths read-only; no access by default.
	fsCfg := wazero.NewFSConfig()
	for _, p := range d.cfg.AllowedPaths {
		fsCfg = fsCfg.WithReadOnlyDirMount(p, p)
	}
	modCfg = modCfg.WithFSConfig(fsCfg)

	// Env vars: expose only explicitly whitelisted variables.
	for _, key := range d.cfg.AllowedEnv {
		if val, ok := os.LookupEnv(key); ok {
			modCfg = modCfg.WithEnv(key, val)
		}
	}
	d.modCfg = modCfg

	// Guard: snapshot modes that use process-wide state are only safe with a
	// single WASM instance.
	if err := d.cfg.validateSnapshotMode(maxInstances); err != nil {
		return fmt.Errorf("wasm: %w", err)
	}

	// Build the pool.
	d.pool = make(chan *wasmSupervisor, maxInstances)

	for i := 0; i < maxInstances; i++ {
		sv := newWasmSupervisor(rt, compiled, modCfg, d.cfg.Timeout, d.cfg.SnapshotMode, d.log)

		if err := sv.Start(ctx); err != nil {
			// Clean up already-started supervisors.
			drainPool(ctx, d.pool, d.log)
			_ = rt.Close(ctx)
			return fmt.Errorf("wasm: start instance %d: %w", i, err)
		}

		d.pool <- sv
	}

	d.log.Info("wasm dispatcher ready", zap.Int("instances", maxInstances))

	return nil
}

// Send acquires a supervisor from the pool, dispatches the request, and
// returns the supervisor to the pool.
func (d *Dispatcher) Send(
	ctx context.Context,
	method string,
	data map[string]any,
) (map[string]any, error) {
	// Acquire a supervisor, honouring the caller's context deadline.
	var sv *wasmSupervisor
	select {
	case sv = <-d.pool:
	case <-ctx.Done():
		return nil, fmt.Errorf("wasm: acquire instance: %w", ctx.Err())
	}

	result, err := sv.Send(ctx, method, data)

	// Return the supervisor to the pool only if it is healthy.
	// If the snapshot restore failed inside Send, sv.healthy is false and the
	// supervisor's state is undefined — discard it and spawn a replacement so
	// pool capacity is eventually restored.
	if sv.IsHealthy() {
		d.pool <- sv
	} else {
		d.log.Warn("wasm supervisor unhealthy after request — dropping from pool, spawning replacement")
		go func() { _ = sv.Shutdown(context.Background()) }()
		go d.spawnOne()
	}

	if err != nil {
		return nil, fmt.Errorf("wasm: send: %w", err)
	}

	return result, nil
}

// spawnOne initialises a fresh wasmSupervisor and adds it to the pool.
// Called in a goroutine when an unhealthy supervisor is discarded so that
// pool capacity is eventually restored. Failures are logged but not fatal.
func (d *Dispatcher) spawnOne() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	d.log.Info("wasm: initialising replacement supervisor")
	sv := newWasmSupervisor(d.rt, d.compiled, d.modCfg, d.cfg.Timeout, d.cfg.SnapshotMode, d.log)
	if err := sv.Start(ctx); err != nil {
		d.log.Error("wasm: replacement supervisor init failed", zap.Error(err))
		return
	}
	d.pool <- sv
	d.log.Info("wasm: replacement supervisor ready")
}

// Shutdown closes all module instances and the wazero runtime.
func (d *Dispatcher) Shutdown(ctx context.Context) error {
	d.log.Debug("shutting down wasm dispatcher")

	drainPool(ctx, d.pool, d.log)

	if d.rt != nil {
		if err := d.rt.Close(ctx); err != nil {
			return fmt.Errorf("wasm: close runtime: %w", err)
		}
		d.rt = nil
	}

	return nil
}

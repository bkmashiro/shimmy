package wasm

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/dispatcher"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"go.uber.org/zap"
)

// ReactorPythonDispatcher is the dispatcher for the python-reactor WASM profile.
// The implementation intentionally keeps a full-memory snapshot strategy only.
type ReactorPythonDispatcher struct {
	cfg Config
	log *zap.Logger

	pool   chan *ReactorPythonRunner
	script string

	rt       wazero.Runtime
	compiled wazero.CompiledModule
	modCfg   wazero.ModuleConfig

	started bool
	closed  bool

	mu       sync.Mutex
	closedCh chan struct{}
	pending  sync.WaitGroup
}

var _ dispatcher.Dispatcher = (*ReactorPythonDispatcher)(nil)

var (
	errReactorPythonNotStarted = errors.New("reactor-python: dispatcher has not been started")
	errReactorPythonShutdown   = errors.New("reactor-python: dispatcher is shut down")
)

func NewReactorPythonDispatcher(cfg Config, log *zap.Logger) *ReactorPythonDispatcher {
	return &ReactorPythonDispatcher{
		cfg:      cfg,
		log:      log.Named("dispatcher_reactor_python"),
		closedCh: make(chan struct{}),
	}
}

// Start validates reactor config, loads the Python script and module bytes,
// compiles the module once, and initialises a warm pool.
func (d *ReactorPythonDispatcher) Start(ctx context.Context) error {
	d.cfg.applyEnv()
	d.cfg.applyDefaults()

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return errReactorPythonShutdown
	}
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()

	scriptPath := strings.TrimSpace(d.cfg.PythonScriptPath)
	if scriptPath == "" {
		return fmt.Errorf("reactor-python: FUNCTION_WASM_PYTHON_SCRIPT is required")
	}

	scriptBytes, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("reactor-python: read script %q: %w", scriptPath, err)
	}

	modulePath := strings.TrimSpace(d.cfg.ModulePath)
	if modulePath == "" {
		return fmt.Errorf("reactor-python: FUNCTION_WASM_MODULE is required")
	}

	wasmBytes, err := os.ReadFile(modulePath)
	if err != nil {
		return fmt.Errorf("reactor-python: read module %q: %w", modulePath, err)
	}

	rtCfg := wazero.NewRuntimeConfig().
		// WithCloseOnContextDone cancels long-running guest code when request
		// contexts timeout.
		WithCloseOnContextDone(true)
	if d.cfg.MaxMemoryPages > 0 {
		rtCfg = rtCfg.WithMemoryLimitPages(d.cfg.MaxMemoryPages)
	}

	if d.cfg.CompileCacheDir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(d.cfg.CompileCacheDir)
		if err != nil {
			d.log.Warn("failed to create wazero compilation cache, continuing without cache",
				zap.String("dir", d.cfg.CompileCacheDir),
				zap.Error(err))
		} else {
			rtCfg = rtCfg.WithCompilationCache(cache)
		}
	}

	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return fmt.Errorf("reactor-python: instantiate wasi: %w", err)
	}
	if err := instantiateEnvModule(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return fmt.Errorf("reactor-python: instantiate env: %w", err)
	}

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx)
		return fmt.Errorf("reactor-python: compile module: %w", err)
	}

	modCfg := wazero.NewModuleConfig().
		WithName("").
		WithSysNanosleep().
		WithSysWalltime().
		WithSysNanotime()

	if len(d.cfg.AllowedPaths) > 0 {
		fsCfg := wazero.NewFSConfig()
		for _, p := range d.cfg.AllowedPaths {
			fsCfg = fsCfg.WithReadOnlyDirMount(p, p)
		}
		modCfg = modCfg.WithFSConfig(fsCfg)
	}
	for _, key := range d.cfg.AllowedEnv {
		if val, ok := os.LookupEnv(key); ok {
			modCfg = modCfg.WithEnv(key, val)
		}
	}

	poolSize := d.cfg.MaxInstances
	if poolSize <= 0 {
		poolSize = runtime.NumCPU()
	}

	runners := make([]*ReactorPythonRunner, 0, poolSize)
	pool := make(chan *ReactorPythonRunner, poolSize)

	for i := 0; i < poolSize; i++ {
		runner := newReactorPythonRunner(rt, compiled, modCfg, string(scriptBytes), d.cfg, d.log)
		if err := runner.Start(ctx); err != nil {
			for _, startedRunner := range runners {
				_ = startedRunner.Shutdown(context.Background())
			}
			_ = rt.Close(ctx)
			return fmt.Errorf("reactor-python: init runner %d: %w", i, err)
		}
		runners = append(runners, runner)
		pool <- runner
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		for _, startedRunner := range runners {
			_ = startedRunner.Shutdown(context.Background())
		}
		_ = rt.Close(ctx)
		return errReactorPythonShutdown
	}

	d.script = string(scriptBytes)
	d.rt = rt
	d.compiled = compiled
	d.modCfg = modCfg
	d.pool = pool
	d.started = true
	d.mu.Unlock()

	d.log.Info("reactor-python dispatcher ready",
		zap.String("script", scriptPath),
		zap.String("module", modulePath),
		zap.Int("pool_size", poolSize),
	)

	return nil
}

// Send dispatches one request to a pooled runner.
func (d *ReactorPythonDispatcher) Send(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	if err := d.tryBeginSend(); err != nil {
		return nil, err
	}
	defer d.pending.Done()

	if method == "" {
		method = "eval"
	}

	var runner *ReactorPythonRunner
	select {
	case runner = <-d.pool:
	case <-d.closedCh:
		return nil, errReactorPythonShutdown
	case <-ctx.Done():
		return nil, fmt.Errorf("reactor-python: acquire runner: %w", ctx.Err())
	}

	defer func() {
		if runner.IsHealthy() {
			d.returnOrDiscard(runner)
		} else {
			d.log.Warn("reactor-python runner unhealthy after request — spawning replacement")
			d.discardAsync(runner)
			d.spawnReplacementAsync()
		}
	}()

	result, err := runner.Send(ctx, method, params)
	if err != nil {
		return nil, fmt.Errorf("reactor-python: send: %w", err)
	}

	return map[string]any{
		"command": method,
		"result":  result,
	}, nil
}

// Shutdown tears down all pooled runners and runtime state. Idempotent.
func (d *ReactorPythonDispatcher) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	close(d.closedCh)
	d.mu.Unlock()

	d.pending.Wait()

	for {
		select {
		case runner := <-d.pool:
			if err := runner.Shutdown(ctx); err != nil {
				d.log.Warn("error shutting down reactor-python runner", zap.Error(err))
			}
		default:
			d.log.Debug("reactor-python pool drained")
			if d.rt != nil {
				err := d.rt.Close(ctx)
				if err != nil {
					return err
				}
			}
			d.mu.Lock()
			d.started = false
			d.rt = nil
			d.compiled = nil
			d.modCfg = nil
			d.script = ""
			d.pool = nil
			d.mu.Unlock()
			return nil
		}
	}
}

// returnOrDiscard puts a healthy runner back in the pool unless shutdown has begun.
func (d *ReactorPythonDispatcher) returnOrDiscard(runner *ReactorPythonRunner) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		d.discardAsync(runner)
		return
	}
	d.pool <- runner
	d.mu.Unlock()
}

func (d *ReactorPythonDispatcher) discardAsync(runner *ReactorPythonRunner) {
	d.pending.Add(1)
	go func() {
		defer d.pending.Done()
		_ = runner.Shutdown(context.Background())
	}()
}

// spawnReplacementAsync starts a fresh runner if dispatcher still open.
func (d *ReactorPythonDispatcher) spawnReplacementAsync() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.pending.Add(1)
	d.mu.Unlock()
	go d.spawnReplacement()
}

func (d *ReactorPythonDispatcher) spawnReplacement() {
	defer d.pending.Done()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	d.mu.Lock()
	rt := d.rt
	compiled := d.compiled
	modCfg := d.modCfg
	script := d.script
	tcfg := d.cfg
	d.mu.Unlock()

	if rt == nil || compiled == nil || modCfg == nil {
		return
	}

	runner := newReactorPythonRunner(rt, compiled, modCfg, script, tcfg, d.log)
	if err := runner.Start(ctx); err != nil {
		d.log.Error("reactor-python: replacement runner init failed", zap.Error(err))
		return
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		d.log.Info("reactor-python: replacement runner born during shutdown")
		_ = runner.Shutdown(context.Background())
		return
	}
	d.pool <- runner
	d.mu.Unlock()
}

func (d *ReactorPythonDispatcher) tryBeginSend() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return errReactorPythonShutdown
	}
	if !d.started {
		return errReactorPythonNotStarted
	}
	d.pending.Add(1)
	return nil
}

// ── Runner ───────────────────────────────────────────────────────────────

// ReactorPythonRunner represents one initialized python-reactor WASM instance.
// It keeps its own snapshot so Send can restore full linear-memory state
// between requests.
type ReactorPythonRunner struct {
	rt       wazero.Runtime
	compiled wazero.CompiledModule
	modCfg   wazero.ModuleConfig
	cfg      Config
	log      *zap.Logger
	script   string

	mod      api.Module
	adapter  *wasmAdapter
	strategy SnapshotStrategy

	healthy      bool
	snapshotSize uint32
	mu           sync.Mutex
}

func newReactorPythonRunner(rt wazero.Runtime, compiled wazero.CompiledModule, modCfg wazero.ModuleConfig, script string, cfg Config, log *zap.Logger) *ReactorPythonRunner {
	return &ReactorPythonRunner{
		rt:       rt,
		compiled: compiled,
		modCfg:   modCfg,
		cfg:      cfg,
		script:   script,
		log:      log.Named("reactor_python"),
	}
}

// Start instantiates one module and captures the post-init linear-memory snapshot.
func (r *ReactorPythonRunner) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.mod != nil {
		return nil
	}

	r.log.Info("instantiating python-reactor module")

	pythonPath := "/usr/lib/python3.14/site-packages"
	if v := os.Getenv("FUNCTION_WASM_PYTHON_PATH"); v != "" {
		pythonPath = v
	}
	var stderr bytes.Buffer
	modCfg := r.modCfg.
		WithName("").
		WithStartFunctions("_initialize").
		WithEnv("PYTHONHOME", "/usr").
		WithEnv("PYTHONDONTWRITEBYTECODE", "1").
		WithEnv("PYTHONPATH", pythonPath).
		WithStderr(&stderr)
	if len(r.cfg.AllowedPaths) > 0 {
		modCfg = modCfg.WithEnv("PYTHONPATH", pythonPath+":"+strings.Join(r.cfg.AllowedPaths, ":"))
	}
	mod, err := r.rt.InstantiateModule(ctx, r.compiled, modCfg)
	if err != nil {
		return fmt.Errorf("reactor-python: instantiate module: %w", err)
	}

	if fnPyInit := mod.ExportedFunction("py_init"); fnPyInit != nil {
		if _, err := fnPyInit.Call(ctx); err != nil {
			_ = mod.Close(ctx)
			return fmt.Errorf("reactor-python: py_init: %w\nstderr: %s", err, stderr.String())
		}
	}

	r.mod = mod
	r.adapter = newWasmAdapter(mod, r.log)
	r.strategy = NewFullMemcpyStrategy()

	if err := r.strategy.Take(mod.Memory()); err != nil {
		_ = mod.Close(ctx)
		r.mod = nil
		r.adapter = nil
		return fmt.Errorf("reactor-python: take snapshot: %w", err)
	}

	if mem := mod.Memory(); mem != nil {
		r.snapshotSize = mem.Size()
	}
	r.healthy = true

	r.log.Info("python-reactor instance ready",
		zap.Uint32("snapshot_bytes", r.snapshotSize),
	)

	return nil
}

// Send sends one request payload to the guest.
func (r *ReactorPythonRunner) Send(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	r.mu.Lock()
	if r.mod == nil || r.adapter == nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("reactor-python: runner is not started")
	}
	r.mu.Unlock()

	if method == "" {
		method = "eval"
	}

	req := map[string]any{
		"script": r.script,
		"method": method,
		"input":  params,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("reactor-python: marshal request: %w", err)
	}

	result, err := r.callEvaluate(ctx, reqBytes)
	r.mu.Lock()
	defer r.mu.Unlock()

	restoreErr := r.restoreSnapshot()
	if restoreErr != nil {
		r.healthy = false
		if err == nil {
			err = fmt.Errorf("reactor-python: restore snapshot: %w", restoreErr)
		}
	}

	return result, err
}

func (r *ReactorPythonRunner) IsHealthy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mod != nil && r.healthy
}

func (r *ReactorPythonRunner) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.mod == nil {
		return nil
	}

	defer func() {
		r.mod = nil
		r.adapter = nil
		r.healthy = false
	}()

	if err := r.mod.Close(ctx); err != nil {
		return fmt.Errorf("reactor-python: close module: %w", err)
	}

	if r.strategy != nil {
		if err := r.strategy.Close(); err != nil {
			return err
		}
	}

	return nil
}

func (r *ReactorPythonRunner) callEvaluate(ctx context.Context, reqBytes []byte) (map[string]any, error) {
	if r.adapter.allocFn == nil {
		return nil, fmt.Errorf("reactor-python: guest module does not export 'alloc'")
	}
	if r.adapter.evalFn == nil {
		return nil, fmt.Errorf("reactor-python: guest module does not export 'evaluate'")
	}

	execCtx := ctx
	if r.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, r.cfg.Timeout)
		defer cancel()
	}

	allocRes, err := r.adapter.allocFn.Call(execCtx, uint64(len(reqBytes)))
	if err != nil {
		return nil, fmt.Errorf("reactor-python: alloc(%d): %w", len(reqBytes), err)
	}
	reqPtr := uint32(allocRes[0])
	if reqPtr == 0 {
		return nil, fmt.Errorf("reactor-python: alloc returned NULL")
	}
	mem := r.mod.Memory()
	if mem == nil {
		return nil, fmt.Errorf("reactor-python: guest module has no linear memory")
	}
	if !mem.Write(reqPtr, reqBytes) {
		return nil, fmt.Errorf("reactor-python: write request JSON at ptr=%d", reqPtr)
	}

	evalRes, err := r.adapter.evalFn.Call(execCtx, uint64(reqPtr), uint64(len(reqBytes)))
	if err != nil {
		return nil, fmt.Errorf("reactor-python: evaluate: %w", err)
	}
	if len(evalRes) == 0 {
		return nil, fmt.Errorf("reactor-python: evaluate returned no response pointer")
	}
	resPtr := uint32(evalRes[0])
	lenBytes, ok := mem.Read(resPtr, 4)
	if !ok {
		return nil, fmt.Errorf("reactor-python: read response length at ptr=%d", resPtr)
	}
	resLen := binary.LittleEndian.Uint32(lenBytes)
	if uint64(resPtr)+4+uint64(resLen) > uint64(mem.Size()) {
		return nil, fmt.Errorf("reactor-python: response out of bounds: resPtr=%d resLen=%d memSize=%d", resPtr, resLen, mem.Size())
	}
	resBytes, ok := mem.Read(resPtr+4, resLen)
	if !ok {
		return nil, fmt.Errorf("reactor-python: read response bytes at ptr=%d len=%d", resPtr+4, resLen)
	}

	var result map[string]any
	if err := json.Unmarshal(resBytes, &result); err != nil {
		return nil, fmt.Errorf("reactor-python: unmarshal response: %w", err)
	}
	return result, nil
}

func (r *ReactorPythonRunner) restoreSnapshot() error {
	if r.mod == nil {
		return nil
	}

	mem := r.mod.Memory()
	if mem == nil {
		return nil
	}

	if err := r.strategy.Restore(mem); err != nil {
		return err
	}

	if cur := mem.Size(); cur > r.snapshotSize {
		tail := make([]byte, cur-r.snapshotSize)
		if !mem.Write(r.snapshotSize, tail) {
			return fmt.Errorf("reactor-python: restore zero-fill failed after memory.grow")
		}
		return fmt.Errorf("reactor-python: memory grew after request: %w", ErrMemoryGrew)
	}

	return nil
}

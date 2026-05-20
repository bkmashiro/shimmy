//go:build linux

package wasm

// ReactorPythonRunner executes Python eval functions via a reactor-mode
// python-reactor.wasm binary.
//
// # Architecture
//
// python-reactor.wasm is compiled with -mexec-model=reactor against
// libpython3.12.a. It exports named functions instead of _start:
//
//	py_init()           – initialise CPython once; defines _handle_request
//	py_exec(ptr, len)   – execute one request (returns after each call)
//	alloc(size) → ptr   – allocate scratch in WASM linear memory
//	dealloc(ptr)        – free scratch allocated by alloc()
//	resp_buf() → ptr    – pointer to the 4 MiB response buffer
//	resp_len() → ptr    – pointer to the int32_t response length
//
// # Snapshot / Restore
//
// After py_init() returns, the entire WASM linear memory is copied into a
// Go []byte snapshot. Before each py_exec() call the snapshot is written back.
// This resets CPython's heap to its exact post-initialisation state, providing
// true per-request interpreter isolation:
//
//   - No sys.modules state from a previous request can bleed in.
//   - User-defined globals, class state, etc. are fully reset.
//   - Python's garbage-collected heap does not grow across requests.
//
// This is safe in reactor mode because py_exec() RETURNS to Go after every
// request. There is no goroutine blocked inside WASM between calls, so
// writing to linear memory is uncontested.
//
// # Host protocol
//
//  1. Restore memory snapshot (linear memory ← post-py_init state).
//  2. ptr ← alloc(len(reqJSON))
//  3. Write reqJSON into memory[ptr:ptr+len]
//  4. py_exec(ptr, len)
//  5. bufPtr ← resp_buf(); lenPtr ← resp_len()
//  6. respLen ← int32 at memory[lenPtr:]
//  7. result ← memory[bufPtr : bufPtr+respLen]
//  8. (dealloc not needed — next restore clears the heap)

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"go.uber.org/zap"
)

// ReactorPythonRunner keeps one python-reactor.wasm instance alive and
// executes requests against it with snapshot/restore isolation.
//
// NOT goroutine-safe: only one request may be in-flight at a time.
// Use ReactorPythonDispatcher (a pool of runners) for concurrent workloads.
type ReactorPythonRunner struct {
	wasmPath string
	cfg      Config
	log      *zap.Logger

	mu          sync.Mutex
	initialized bool
	closed      bool

	rt       wazero.Runtime
	mod      api.Module
	strategy SnapshotStrategy // snapshot taken after py_init(); restored before each py_exec()

	// Cached exported functions.
	fnPyInit  api.Function
	fnPyExec  api.Function
	fnAlloc   api.Function
	fnDealloc api.Function
	fnRespBuf api.Function
	fnRespLen api.Function
}

// NewReactorPythonRunner creates a ReactorPythonRunner.
// Call Init before SendRequest.
func NewReactorPythonRunner(wasmPath string, cfg Config, log *zap.Logger) *ReactorPythonRunner {
	cfg.applyDefaults()
	return &ReactorPythonRunner{
		wasmPath: wasmPath,
		cfg:      cfg,
		log:      log.Named("reactor_python"),
	}
}

// Init loads python-reactor.wasm, instantiates it in reactor mode (calling
// _initialize), invokes py_init() to start CPython, then snapshots linear
// memory. This is the expensive step (~7-8 s for Python startup).
func (r *ReactorPythonRunner) Init(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.initialized {
		return nil
	}
	if r.wasmPath == "" {
		return fmt.Errorf("reactor python: wasmPath must be set")
	}

	r.log.Info("reading python-reactor.wasm", zap.String("path", r.wasmPath))
	wasmBytes, err := os.ReadFile(r.wasmPath)
	if err != nil {
		return fmt.Errorf("reactor python: read %q: %w", r.wasmPath, err)
	}

	rtCfg := wazero.NewRuntimeConfig()
	if r.cfg.MaxMemoryPages > 0 {
		rtCfg = rtCfg.WithMemoryLimitPages(r.cfg.MaxMemoryPages)
	}
	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return fmt.Errorf("reactor python: instantiate wasi: %w", err)
	}

	// CPython 3.14 WASM built with WASI SDK 33 imports ~90 symbols from the
	// "env" host module: dynamic-linking stubs, numpy complex math, float16
	// helpers, float status setters, and random hypergeometric stubs.
	// All are registered by instantiateEnvModule.
	if err := instantiateEnvModule(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return fmt.Errorf("reactor python: instantiate env module: %w", err)
	}

	r.log.Info("compiling python-reactor.wasm (~1-2 s)")
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx)
		return fmt.Errorf("reactor python: compile: %w", err)
	}
	defer func() { _ = compiled.Close(ctx) }()

	// Reactor mode: wazero calls _initialize (not _start) on instantiation.
	// PYTHONHOME tells CPython where to find the stdlib that wasi-vfs packed
	// at /usr/lib/python3.x inside the WASM binary.
	var stderrBuf bytes.Buffer
	mc := wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_initialize").
		WithEnv("PYTHONHOME", "/usr").
		WithEnv("PYTHONDONTWRITEBYTECODE", "1").
		WithStderr(&stderrBuf).
		WithSysNanosleep().
		WithSysWalltime().
		WithSysNanotime()

	// Mount read-only host paths into the WASM sandbox.
	// AllowedPaths is used to expose wasi-wheels site-packages directories
	// (or any other read-only data) to the Python interpreter.
	// Each path is mounted at the same absolute location inside the sandbox.
	if len(r.cfg.AllowedPaths) > 0 {
		fsCfg := wazero.NewFSConfig()
		for _, p := range r.cfg.AllowedPaths {
			fsCfg = fsCfg.WithReadOnlyDirMount(p, p)
		}
		mc = mc.WithFSConfig(fsCfg)
		// Tell Python where to find the extra packages.
		mc = mc.WithEnv("PYTHONPATH", strings.Join(r.cfg.AllowedPaths, ":"))
	}

	r.log.Info("instantiating (reactor mode, _initialize)...")
	mod, err := rt.InstantiateModule(ctx, compiled, mc)
	if err != nil {
		_ = rt.Close(ctx)
		return fmt.Errorf("reactor python: instantiate module: %w", err)
	}

	r.rt = rt
	r.mod = mod

	// Cache and validate exported functions.
	exports := map[string]*api.Function{
		"py_init":  &r.fnPyInit,
		"py_exec":  &r.fnPyExec,
		"alloc":    &r.fnAlloc,
		"dealloc":  &r.fnDealloc,
		"resp_buf": &r.fnRespBuf,
		"resp_len": &r.fnRespLen,
	}
	for name, dst := range exports {
		fn := mod.ExportedFunction(name)
		if fn == nil {
			_ = r.closeAll(ctx)
			return fmt.Errorf("reactor python: missing required export %q", name)
		}
		*dst = fn
	}

	// Call py_init() to start CPython and define _handle_request.
	r.log.Info("calling py_init() to initialise CPython (~7-8 s)...")
	if _, err := r.fnPyInit.Call(ctx); err != nil {
		stderr := stderrBuf.String()
		_ = r.closeAll(ctx)
		return fmt.Errorf("reactor python: py_init(): %w\nstderr: %s", err, stderr)
	}
	r.log.Info("py_init() complete — CPython is ready")

	// Create snapshot strategy based on config.
	s, err := r.newSnapshotStrategy(mod.Memory())
	if err != nil {
		r.log.Warn("requested snapshot strategy unavailable, falling back to memcpy",
			zap.Error(err))
		s = NewFullMemcpyStrategy()
	}
	r.strategy = s
	if err := r.strategy.Take(mod.Memory()); err != nil {
		_ = r.closeAll(ctx)
		return fmt.Errorf("reactor python: take snapshot: %w", err)
	}
	r.log.Info("memory snapshot taken",
		zap.String("strategy", fmt.Sprintf("%T", r.strategy)),
		zap.Uint32("mem_size", mod.Memory().Size()),
	)

	r.initialized = true
	return nil
}

// SendRequest restores the post-init memory snapshot, writes the request JSON
// into WASM memory via alloc(), calls py_exec(), and reads the JSON response.
//
// The script, method, and inputJSON fields follow the same convention as
// ResidentPythonRunner.SendRequest.
func (r *ReactorPythonRunner) SendRequest(ctx context.Context, script, method string, inputJSON string) (map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.initialized || r.closed {
		return nil, fmt.Errorf("reactor python: not initialized — call Init first")
	}

	reqTimeout := r.cfg.Timeout
	if reqTimeout == 0 {
		reqTimeout = 30 * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, reqTimeout)
	defer cancel()

	if method == "" {
		method = "eval"
	}

	// Build request JSON.
	reqObj := map[string]any{
		"script": script,
		"method": method,
		"input":  json.RawMessage(inputJSON),
	}
	reqBytes, err := json.Marshal(reqObj)
	if err != nil {
		return nil, fmt.Errorf("reactor python: marshal request: %w", err)
	}

	// ── Step 1: Restore snapshot ──────────────────────────────────────────
	// Write the post-py_init() memory image back into WASM linear memory.
	// This resets the Python heap, _resp_buf, _resp_len, and all CPython globals
	// to their exact post-initialisation values. py_exec() will then run in a
	// clean interpreter as if py_init() just completed.
	if err := r.strategy.Restore(r.mod.Memory()); err != nil {
		return nil, fmt.Errorf("reactor python: restore snapshot: %w", err)
	}

	// ── Step 2: Allocate scratch for request JSON ─────────────────────────
	allocRes, err := r.fnAlloc.Call(execCtx, uint64(len(reqBytes)))
	if err != nil {
		return nil, fmt.Errorf("reactor python: alloc(%d): %w", len(reqBytes), err)
	}
	reqPtr := uint32(allocRes[0])
	if reqPtr == 0 {
		return nil, fmt.Errorf("reactor python: alloc returned NULL")
	}

	// ── Step 3: Write request JSON ────────────────────────────────────────
	if !r.mod.Memory().Write(reqPtr, reqBytes) {
		return nil, fmt.Errorf("reactor python: write request JSON to memory at ptr=%d", reqPtr)
	}

	// ── Step 4: Execute ───────────────────────────────────────────────────
	if _, err := r.fnPyExec.Call(execCtx, uint64(reqPtr), uint64(len(reqBytes))); err != nil {
		return nil, fmt.Errorf("reactor python: py_exec: %w", err)
	}

	// ── Step 5: Read response ─────────────────────────────────────────────
	bufPtrRes, err := r.fnRespBuf.Call(execCtx)
	if err != nil {
		return nil, fmt.Errorf("reactor python: resp_buf: %w", err)
	}
	lenPtrRes, err := r.fnRespLen.Call(execCtx)
	if err != nil {
		return nil, fmt.Errorf("reactor python: resp_len: %w", err)
	}

	bufPtr := uint32(bufPtrRes[0])
	lenPtr := uint32(lenPtrRes[0])

	// Read int32_t response length (little-endian).
	lenRaw, ok := r.mod.Memory().Read(lenPtr, 4)
	if !ok {
		return nil, fmt.Errorf("reactor python: read resp_len at ptr=%d", lenPtr)
	}
	respLen := int32(binary.LittleEndian.Uint32(lenRaw))
	if respLen <= 0 {
		return nil, fmt.Errorf("reactor python: resp_len=%d (py_exec wrote no response)", respLen)
	}

	// Read response JSON bytes.
	respBytes, ok := r.mod.Memory().Read(bufPtr, uint32(respLen))
	if !ok {
		return nil, fmt.Errorf("reactor python: read response bytes at ptr=%d len=%d", bufPtr, respLen)
	}

	result, err := parseJSONResponse(string(respBytes))
	if err != nil {
		return nil, fmt.Errorf("reactor python: parse response: %w; raw: %.200s", err, respBytes)
	}
	if errMsg, ok := result["error"].(string); ok {
		return nil, fmt.Errorf("reactor python script error: %s", errMsg)
	}
	return result, nil
}

// closeAll closes the module and runtime. Must be called with r.mu held.
func (r *ReactorPythonRunner) closeAll(ctx context.Context) error {
	if r.mod != nil {
		_ = r.mod.Close(ctx)
		r.mod = nil
	}
	if r.rt != nil {
		_ = r.rt.Close(ctx)
		r.rt = nil
	}
	return nil
}

// Shutdown closes the WASM module and runtime.
func (r *ReactorPythonRunner) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}
	r.log.Debug("shutting down reactor python runner")
	r.closed = true
	r.initialized = false
	if r.strategy != nil {
		_ = r.strategy.Close()
	}
	return r.closeAll(ctx)
}

// newSnapshotStrategy creates the SnapshotStrategy requested by r.cfg.SnapshotMode.
// Returns an error if the strategy is unavailable; the caller should fall back
// to NewFullMemcpyStrategy().
func (r *ReactorPythonRunner) newSnapshotStrategy(mem api.Memory) (SnapshotStrategy, error) {
	switch r.cfg.SnapshotMode {
	case "soft-dirty":
		return NewSoftDirtyStrategy(mem)
	case "mprotect":
		return NewMprotectStrategy(mem)
	case "uffd":
		return NewUffdStrategy(mem)
	default:
		// "memcpy" or "" — always-available baseline.
		return NewFullMemcpyStrategy(), nil
	}
}

// ── Dispatcher ───────────────────────────────────────────────────────────────

// ReactorPythonDispatcher implements dispatcher.Dispatcher using a pool of
// ReactorPythonRunner instances backed by python-reactor.wasm.
//
// Each runner initialises CPython once (~7-8 s) and then handles requests with
// true per-request interpreter reset via snapshot/restore (~20-100 ms/request).
//
// Pool size defaults to min(runtime.NumCPU(), 4) since each runner consumes
// ~100 MB of WASM linear memory (CPython heap after py_init).
type ReactorPythonDispatcher struct {
	cfg    Config
	log    *zap.Logger
	pool   chan *ReactorPythonRunner
	script string
}

// NewReactorPythonDispatcher creates a ReactorPythonDispatcher. Call Start first.
func NewReactorPythonDispatcher(cfg Config, log *zap.Logger) *ReactorPythonDispatcher {
	return &ReactorPythonDispatcher{
		cfg: cfg,
		log: log.Named("dispatcher_reactor_python"),
	}
}

// Start reads the eval script and initialises all pool runners in parallel.
func (d *ReactorPythonDispatcher) Start(ctx context.Context) error {
	d.cfg.applyEnv()
	d.cfg.applyDefaults()

	if d.cfg.PythonScriptPath == "" {
		return fmt.Errorf("reactor-python: PythonScriptPath must be set (FUNCTION_WASM_PYTHON_SCRIPT)")
	}
	scriptBytes, err := os.ReadFile(d.cfg.PythonScriptPath)
	if err != nil {
		return fmt.Errorf("reactor-python: read script %q: %w", d.cfg.PythonScriptPath, err)
	}
	d.script = string(scriptBytes)

	poolSize := d.cfg.MaxInstances
	if poolSize <= 0 {
		poolSize = runtime.NumCPU()
	}
	if poolSize > 4 {
		poolSize = 4 // each runner uses ~100 MB; cap conservatively
	}

	d.log.Info("starting reactor-python dispatcher",
		zap.String("module", d.cfg.ModulePath),
		zap.String("script", d.cfg.PythonScriptPath),
		zap.Int("pool_size", poolSize),
	)

	d.pool = make(chan *ReactorPythonRunner, poolSize)

	type result struct {
		runner *ReactorPythonRunner
		err    error
		index  int
	}
	results := make([]result, poolSize)
	var wg sync.WaitGroup
	wg.Add(poolSize)

	for i := 0; i < poolSize; i++ {
		i := i
		go func() {
			defer wg.Done()
			runner := NewReactorPythonRunner(d.cfg.ModulePath, d.cfg, d.log)
			if err := runner.Init(ctx); err != nil {
				results[i] = result{index: i, err: err}
				return
			}
			results[i] = result{index: i, runner: runner}
		}()
	}
	wg.Wait()

	var firstErr error
	var started []*ReactorPythonRunner
	for _, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("reactor-python: init runner %d: %w", r.index, r.err)
			}
		} else {
			started = append(started, r.runner)
		}
	}
	if firstErr != nil {
		for _, runner := range started {
			_ = runner.Shutdown(ctx)
		}
		return firstErr
	}
	for _, runner := range started {
		d.pool <- runner
	}

	d.log.Info("reactor-python dispatcher ready", zap.Int("pool_size", poolSize))
	return nil
}

// Send dispatches a request to a pooled ReactorPythonRunner.
func (d *ReactorPythonDispatcher) Send(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	if method == "healthcheck" {
		return map[string]any{
			"command": "healthcheck",
			"result":  map[string]any{"status": "ok"},
		}, nil
	}

	var runner *ReactorPythonRunner
	select {
	case runner = <-d.pool:
	case <-ctx.Done():
		return nil, fmt.Errorf("reactor-python: acquire runner: %w", ctx.Err())
	}
	defer func() { d.pool <- runner }()

	inputJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("reactor-python: marshal params: %w", err)
	}

	result, err := runner.SendRequest(ctx, d.script, method, string(inputJSON))
	if err != nil {
		return nil, fmt.Errorf("reactor-python: send request: %w", err)
	}
	return map[string]any{
		"command": method,
		"result":  result,
	}, nil
}

// Shutdown drains the pool and shuts down each runner.
func (d *ReactorPythonDispatcher) Shutdown(ctx context.Context) error {
	d.log.Debug("shutting down reactor-python dispatcher")
	if d.pool == nil {
		return nil
	}
	var firstErr error
	for i := 0; i < cap(d.pool); i++ {
		runner := <-d.pool
		if err := runner.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

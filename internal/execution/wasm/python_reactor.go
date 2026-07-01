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
//	evaluate(ptr, len)   – optional target ABI wrapper around py_exec; returns
//	                      pointer to [uint32 length][JSON response]
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
	// wasmBytes, when non-nil, is used in place of reading wasmPath from disk
	// during Init. The dispatcher pre-loads the 242 MB python-reactor.wasm
	// once and shares the slice across every pooled runner (and replacement
	// spawns) so the file is not re-read per runner.
	wasmBytes []byte
	cfg       Config
	log       *zap.Logger

	mu          sync.Mutex
	initialized bool
	closed      bool

	stderrBuf bytes.Buffer // CPython stderr — readable from SendRequest errors

	rt       wazero.Runtime
	mod      api.Module
	strategy SnapshotStrategy // snapshot taken after py_init(); restored before each py_exec()

	// Cached exported functions.
	fnPyInit   api.Function
	fnPyExec   api.Function
	fnEvaluate api.Function // optional target ABI wrapper; falls back to py_exec when absent
	fnAlloc    api.Function
	fnDealloc  api.Function
	fnRespBuf  api.Function
	fnRespLen  api.Function
}

// IsHealthy reports whether the runner is initialised and not closed.
// A runner becomes unhealthy when its WASM module is closed by wazero after
// a context timeout (WithCloseOnContextDone). The dispatcher uses this to
// decide whether to return a runner to the pool or discard it.
func (r *ReactorPythonRunner) IsHealthy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.initialized && !r.closed
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

// newReactorPythonRunnerWithBytes is like NewReactorPythonRunner but reuses
// an already-loaded copy of the .wasm bytes. The dispatcher pre-loads
// python-reactor.wasm once and hands the same slice to every pool runner so
// the 242 MB file is not re-read per runner. wasmPath is retained for
// diagnostics only.
func newReactorPythonRunnerWithBytes(wasmPath string, wasmBytes []byte, cfg Config, log *zap.Logger) *ReactorPythonRunner {
	r := NewReactorPythonRunner(wasmPath, cfg, log)
	r.wasmBytes = wasmBytes
	return r
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

	wasmBytes := r.wasmBytes
	if wasmBytes == nil {
		if r.wasmPath == "" {
			return fmt.Errorf("reactor python: wasmPath must be set")
		}
		r.log.Info("reading python-reactor.wasm", zap.String("path", r.wasmPath))
		b, err := os.ReadFile(r.wasmPath)
		if err != nil {
			return fmt.Errorf("reactor python: read %q: %w", r.wasmPath, err)
		}
		wasmBytes = b
	} else {
		r.log.Debug("reusing pre-loaded python-reactor.wasm bytes", zap.Int("size", len(wasmBytes)))
	}

	rtCfg := wazero.NewRuntimeConfig().
		// WithCloseOnContextDone causes wazero to interrupt a running WASM module
		// when the call context is cancelled or times out. Without this flag, a
		// Python infinite loop would block the runner goroutine indefinitely even
		// after context.WithTimeout fires. When the module is interrupted it is
		// permanently closed; the runner is then marked unhealthy and dropped from
		// the pool by ReactorPythonDispatcher.
		WithCloseOnContextDone(true)

	if r.cfg.CompileCacheDir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(r.cfg.CompileCacheDir)
		if err != nil {
			r.log.Warn("failed to create wazero compilation cache, continuing without cache",
				zap.String("dir", r.cfg.CompileCacheDir),
				zap.Error(err))
		} else {
			rtCfg = rtCfg.WithCompilationCache(cache)
			r.log.Info("wazero compilation cache enabled", zap.String("dir", r.cfg.CompileCacheDir))
		}
	}

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

	// Compile module (instant on cache hit; ~1-3 min cold for 242 MB binary).
	if r.cfg.CompileCacheDir == "" {
		r.log.Info("compiling python-reactor.wasm (~1-3 min cold, set FUNCTION_WASM_COMPILE_CACHE to skip on repeat starts)")
	}
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx)
		return fmt.Errorf("reactor python: compile: %w", err)
	}
	defer func() { _ = compiled.Close(ctx) }()

	// Reactor mode: wazero calls _initialize (not _start) on instantiation.
	// PYTHONHOME tells CPython where to find the stdlib that wasi-vfs packed
	// at /usr/lib/python3.x inside the WASM binary.
	r.stderrBuf.Reset()

	// WASI CPython builds typically skip site.py (Py_NoSiteFlag=1), so
	// site-packages is not added to sys.path automatically.  We set PYTHONPATH
	// to the standard site-packages directory packed by wasi-vfs so that
	// built-in packages (e.g. numpy) are importable.
	// The path can be overridden via FUNCTION_WASM_PYTHON_PATH.
	pythonPath := "/usr/lib/python3.14/site-packages"
	if v := os.Getenv("FUNCTION_WASM_PYTHON_PATH"); v != "" {
		pythonPath = v
	}

	mc := wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_initialize").
		WithEnv("PYTHONHOME", "/usr").
		WithEnv("PYTHONDONTWRITEBYTECODE", "1").
		WithEnv("PYTHONPATH", pythonPath).
		WithStderr(&r.stderrBuf).
		WithSysNanosleep().
		WithSysWalltime().
		WithSysNanotime()

	// Mount additional read-only host paths into the WASM sandbox.
	// Used to expose external wasi-wheels site-packages directories.
	// Each path is appended to PYTHONPATH so Python can find the packages.
	if len(r.cfg.AllowedPaths) > 0 {
		fsCfg := wazero.NewFSConfig()
		for _, p := range r.cfg.AllowedPaths {
			fsCfg = fsCfg.WithReadOnlyDirMount(p, p)
		}
		mc = mc.WithFSConfig(fsCfg)
		mc = mc.WithEnv("PYTHONPATH", pythonPath+":"+strings.Join(r.cfg.AllowedPaths, ":"))
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
	// Newer python-reactor builds expose the generic Shimmy WASM ABI entrypoint
	// evaluate(ptr, len) -> ptr-to-[len][JSON]. It is optional so older bundled
	// python-reactor.wasm artifacts that only expose py_exec/resp_buf/resp_len
	// continue to run.
	r.fnEvaluate = mod.ExportedFunction("evaluate")

	// Call py_init() to start CPython and define _handle_request.
	r.log.Info("calling py_init() to initialise CPython (~7-8 s)...")
	if _, err := r.fnPyInit.Call(ctx); err != nil {
		_ = r.closeAll(ctx)
		return fmt.Errorf("reactor python: py_init(): %w\nstderr: %s", err, r.stderrBuf.String())
	}
	r.log.Info("py_init() complete — CPython is ready")

	// Inject site-packages into sys.path before taking the snapshot.
	//
	// WASI CPython builds typically set Py_IgnoreEnvironmentFlag=1 (skipping
	// PYTHONPATH) and do not run site.py, so site-packages is absent from
	// sys.path by default.  We call py_exec with a tiny setup script that
	// directly mutates sys.path; the snapshot taken immediately afterwards
	// captures the updated path, which is then inherited by every request
	// without per-request overhead.
	if err := r.initSysPath(ctx); err != nil {
		r.log.Warn("sys.path injection failed — built-in packages may not be importable",
			zap.Error(err))
	}

	// Create snapshot strategy based on config. selectSnapshotStrategy handles
	// fallback to memcpy internally when the requested strategy is unavailable.
	r.strategy = r.newSnapshotStrategy(mod.Memory())
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
// into WASM memory via alloc(), executes one request, and reads the JSON
// response. New python-reactor modules use the same host-facing per-request ABI
// as generic WASM (alloc + evaluate). Older modules fall back to the historical
// py_exec + resp_buf/resp_len exports.
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
		// Restore failure means the WASM module's memory state is undefined.
		// Mark the runner closed so IsHealthy() returns false and the dispatcher
		// discards rather than returning it to the pool.
		r.closed = true
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

	if r.fnEvaluate != nil {
		return r.callEvaluateABI(execCtx, reqPtr, len(reqBytes))
	}
	return r.callPyExecABI(execCtx, reqPtr, len(reqBytes), reqTimeout)
}

func (r *ReactorPythonRunner) callEvaluateABI(ctx context.Context, reqPtr uint32, reqLen int) (map[string]any, error) {
	// ── Step 4: Execute via target generic-WASM ABI ───────────────────────
	evalRes, err := r.fnEvaluate.Call(ctx, uint64(reqPtr), uint64(reqLen))
	if err != nil {
		if ctx.Err() != nil {
			r.closed = true
			return nil, fmt.Errorf("reactor python: evaluate timed out or was cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("reactor python: evaluate: %w\nstderr: %s", err, r.stderrBuf.String())
	}
	if len(evalRes) == 0 {
		return nil, fmt.Errorf("reactor python: evaluate returned no response pointer")
	}
	return r.readLengthPrefixedJSONResponse(uint32(evalRes[0]))
}

func (r *ReactorPythonRunner) callPyExecABI(ctx context.Context, reqPtr uint32, reqLen int, reqTimeout time.Duration) (map[string]any, error) {
	// ── Step 4: Execute via legacy python-reactor ABI ─────────────────────
	if _, err := r.fnPyExec.Call(ctx, uint64(reqPtr), uint64(reqLen)); err != nil {
		if ctx.Err() != nil {
			// The request context timed out or was cancelled. WithCloseOnContextDone
			// caused wazero to permanently close the module — mark the runner closed
			// so the dispatcher discards it rather than returning it to the pool.
			r.closed = true
			return nil, fmt.Errorf("reactor python: py_exec timed out after %s — script likely contains an infinite loop", reqTimeout)
		}
		return nil, fmt.Errorf("reactor python: py_exec: %w\nstderr: %s", err, r.stderrBuf.String())
	}

	// ── Step 5: Read response ─────────────────────────────────────────────
	bufPtrRes, err := r.fnRespBuf.Call(ctx)
	if err != nil {
		return nil, fmt.Errorf("reactor python: resp_buf: %w", err)
	}
	lenPtrRes, err := r.fnRespLen.Call(ctx)
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
	// Return Python-level errors as structured results rather than Go errors.
	// Callers (dispatcher, CLI) can inspect result["error"] and result["error_type"]
	// to distinguish user-script exceptions from infrastructure failures.
	// We only promote to a Go error for init-time scripts (runInitScript) where
	// the result["error"] is always a plain string with no extra fields.
	return result, nil
}

func (r *ReactorPythonRunner) readLengthPrefixedJSONResponse(resPtr uint32) (map[string]any, error) {
	mem := r.mod.Memory()
	if mem == nil {
		return nil, fmt.Errorf("reactor python: guest module has no linear memory")
	}
	lenBytes, ok := mem.Read(resPtr, 4)
	if !ok {
		return nil, fmt.Errorf("reactor python: read evaluate response length at ptr=%d", resPtr)
	}
	resLen := binary.LittleEndian.Uint32(lenBytes)
	if resLen == 0 {
		return nil, fmt.Errorf("reactor python: evaluate returned empty response")
	}
	if uint64(resPtr)+4+uint64(resLen) > uint64(mem.Size()) {
		return nil, fmt.Errorf("reactor python: evaluate response out of bounds: resPtr=%d resLen=%d memSize=%d", resPtr, resLen, mem.Size())
	}
	resBytes, ok := mem.Read(resPtr+4, resLen)
	if !ok {
		return nil, fmt.Errorf("reactor python: read evaluate response bytes at ptr=%d len=%d", resPtr+4, resLen)
	}
	result, err := parseJSONResponse(string(resBytes))
	if err != nil {
		return nil, fmt.Errorf("reactor python: parse evaluate response: %w; raw: %.200s", err, resBytes)
	}
	return result, nil
}

// initSysPath installs a wasi-vfs–aware meta path finder, stubs numpy.random,
// and pre-warms numpy before taking the snapshot.
//
// Two-phase design
// ─────────────────
// Phase 1 (initSysPathPhase1): sys.path, meta-path finder, numpy test stubs,
//
//	numpy.random package stubs.  Updates _base_modules so the stubs survive
//	the per-request eviction pass.  Must not import numpy (avoids crash risk).
//
// Phase 2 (initSysPathPhase2): imports numpy so the snapshot captures it in
//
//	sys.modules.  If this phase crashes (WASM unreachable), Phase 1 results
//	are preserved and numpy is still importable per-request from the binary.
//
// Splitting the script means a crash in phase 2 does not swallow the phase 1
// setup, and the log clearly identifies which phase failed.
//
// # Problem 1 — scandir
//
// wasi-vfs supports path_open (reading individual files) but NOT fd_readdir
// (directory listing via scandir/listdir).  Python's standard FileFinder
// discovers packages by calling os.scandir() on each sys.path entry.  When
// scandir fails on the site-packages directory, Python's FileFinder reports
// every package as absent — even though the files are readable via open().
//
// Fix: probe package existence with open() instead of scandir.
//
// # Problem 2 — BuiltinImporter skips submodule imports
//
// Numpy C extensions are registered as built-in modules via
// PyImport_AppendInittab("numpy.core._multiarray_umath", ...) etc.
// However, BuiltinImporter.find_spec() returns None when path is not None
// (i.e. when importing a submodule such as numpy.core._multiarray_umath).
// This causes PathFinder to try to dlopen() the extension as a .so file,
// which WASI does not support and returns "unknown dlopen() error".
//
// Fix: check _imp.is_builtin(fullname) in _WasivfsFinder and return a
// BuiltinImporter spec, bypassing PathFinder entirely.
//
// WASI CPython also sets Py_IgnoreEnvironmentFlag=1, so PYTHONPATH env vars
// are ignored.  We mutate sys.path directly to add site-packages candidates.
// initSysPathPhase1 sets up sys.path, the wasi-vfs meta-path finder, numpy
// test-extension stubs, and numpy.random stubs.  It updates _base_modules so
// all stubs survive the per-request eviction pass inside _handle_request.
// No numpy import here — that lives in phase 2 so a crash there is isolated.
const initSysPathPhase1 = `
import sys as _sys, importlib.util as _ilu, importlib.machinery as _ilm, _imp as _imp_mod

# ── 1. Add site-packages candidates to sys.path ──────────────────────────────
for _p in ['/usr/lib/python3.14/site-packages',
           '/usr/local/lib/python3.14/site-packages']:
    if _p not in _sys.path:
        _sys.path.insert(0, _p)

# ── 2. Install a scandir-free meta path finder ───────────────────────────────
_SITE = '/usr/lib/python3.14/site-packages'

class _WasivfsFinder:
    """Meta-path finder for wasi-vfs packed site-packages.

    Handles two cases:
    (a) Built-in C extensions (e.g. numpy.core._multiarray_umath) registered
        via PyImport_AppendInittab.  BuiltinImporter skips these when path is
        not None (submodule import context); we catch them here so PathFinder
        never attempts a dlopen() that WASI cannot fulfil.
    (b) Pure-Python packages / modules packed into wasi-vfs site-packages.
        Uses open() instead of scandir() to probe file existence.
    """
    @staticmethod
    def _exists(path):
        try:
            # M-7 fix: use with-statement to guarantee fd is closed even
            # under non-refcount GC (PyPy) or gc.disable().
            with open(path, 'rb'):
                return True
        except OSError:
            return False

    def find_spec(self, fullname, path, target=None):
        # (a) Built-in C extension?
        if _imp_mod.is_builtin(fullname):
            return _ilu.spec_from_loader(fullname, _ilm.BuiltinImporter)
        # (b) Pure-Python package or module in wasi-vfs site-packages.
        parts = fullname.split('.')
        base  = _SITE + '/' + '/'.join(parts)
        # Package: base/__init__.py
        init = base + '/__init__.py'
        if self._exists(init):
            loader = _ilm.SourceFileLoader(fullname, init)
            return _ilu.spec_from_file_location(
                fullname, init,
                loader=loader,
                submodule_search_locations=[base])
        # Module: base.py
        src = base + '.py'
        if self._exists(src):
            loader = _ilm.SourceFileLoader(fullname, src)
            return _ilu.spec_from_file_location(fullname, src, loader=loader)
        return None

# Insert BEFORE PathFinder (last in meta_path by default) so our finder
# intercepts C-extension and VFS-package lookups before PathFinder can
# attempt a dlopen() that WASI does not support.
# Remove PathFinder from its current position (wherever the binary put it),
# append our finder, then re-append PathFinder.  This guarantees the order:
#   [BuiltinImporter, FrozenImporter, ..., _WasivfsFinder, PathFinder]
# regardless of what the binary's HANDLER_SRC has already done to meta_path.
_sys.meta_path = [_f for _f in _sys.meta_path if _f is not _ilm.PathFinder]
_sys.meta_path.append(_WasivfsFinder())
_sys.meta_path.append(_ilm.PathFinder)

# ── 2b. Fail-loud finder for known-unavailable packages ───────────────────────
# When a user script imports a package that is unavailable in this sandbox,
# raise ImportError with a clear explanation rather than the generic
# "No module named 'X'" message.
#
# Inserted just before PathFinder so all valid finders (BuiltinImporter,
# FrozenImporter, _WasivfsFinder) get first chance; only packages that
# none of those finders recognise reach this check.
class _UnavailablePackageFinder:
    # Store data as class attributes so they survive 'del _UnavailablePackageFinder'
    # (the instance lives in sys.meta_path; self._UNAVAILABLE still works).
    _UNAVAILABLE = {
        'scipy':        'scipy requires compiled C extensions; use a Pyodide-based runtime instead.',
        'pandas':       'pandas requires compiled C extensions; use standard Python or Pyodide.',
        'matplotlib':   'matplotlib requires a display backend unavailable in WASI.',
        'sklearn':      'scikit-learn requires compiled C extensions.',
        'scikit_learn': 'scikit-learn requires compiled C extensions.',
        'torch':        'PyTorch is not available in the WASI sandbox.',
        'tensorflow':   'TensorFlow is not available in the WASI sandbox.',
        'keras':        'Keras/TensorFlow is not available in the WASI sandbox.',
        'PIL':          'Pillow requires compiled C extensions.',
        'cv2':          'OpenCV requires compiled C extensions.',
        'requests':     'Network access is disabled in the WASI sandbox.',
        'httpx':        'Network access is disabled in the WASI sandbox.',
        'aiohttp':      'Network access is disabled in the WASI sandbox.',
        'sqlalchemy':   'SQLAlchemy is not available in the WASI sandbox.',
        'psycopg2':     'psycopg2 requires compiled C extensions.',
        'seaborn':      'seaborn depends on matplotlib and statsmodels; use Pyodide instead.',
    }
    _AVAILABLE = 'Available packages: numpy, sympy, and the Python standard library (no network).'

    def find_spec(self, fullname, path, target=None):
        root = fullname.split('.')[0]
        msg = self._UNAVAILABLE.get(root)
        if msg:
            raise ImportError(
                f"'{fullname}' is not available in the shimmy-wasm sandbox.\n"
                f"Reason: {msg}\n"
                f"{self._AVAILABLE}"
            )
        return None

_sys.meta_path.insert(-1, _UnavailablePackageFinder())
del _UnavailablePackageFinder

# ── 3. Stub numpy test-only C extensions ─────────────────────────────────────
# numpy/core/_add_newdocs.py imports test-only C extensions
# (e.g. numpy.core._multiarray_tests) solely to attach docstrings via
# add_newdoc().  These test extensions are not compiled into the WASI binary.
# Without a stub they fall through to PathFinder → dlopen() → ImportError.
#
# We inject empty module stubs into sys.modules and extend _base_modules
# (defined in __main__ by HANDLER_SRC) so the stubs survive the per-request
# eviction pass and persist in the memory snapshot.
import types as _types
import __main__ as _main

_NUMPY_STUBS = [
    'numpy.core._multiarray_tests',
    'numpy.core._umath_tests',
    'numpy.core._rational_tests',
    'numpy.core._struct_ufunc_tests',
    'numpy.core._operand_flag_tests',
]
for _n in _NUMPY_STUBS:
    if _n not in _sys.modules:
        _m = _types.ModuleType(_n)
        # _add_newdocs.py calls getattr(module, func_name) to attach docstrings.
        # The module has no real functions, so __getattr__ returns a dummy callable.
        _m.__getattr__ = lambda _attr: (lambda *_a, **_kw: None)
        _sys.modules[_n] = _m

# ── 4. Stub numpy.random (package + all C extension submodules) ───────────────
# All numpy.random Cython extensions (bit_generator, _mt19937, mtrand, etc.)
# call Py_FatalError() -> abort() -> WASM unreachable during their PyInit_*
# functions in the WASI sandbox.  The root cause is Cython's vtable
# initialisation dereferencing function pointers that are invalid in WASI.
#
# Additionally, numpy/random/__init__.py itself executes during "import numpy"
# and crashes with unreachable somewhere in its body, leaving numpy.random
# partially initialised in sys.modules (without seed, rand, etc.).
#
# Universal fix: pre-populate sys.modules with pure-Python stubs for:
#   • numpy.random        — the package itself (prevents __init__.py running)
#   • numpy.random.*      — every C extension submodule
# Python checks sys.modules before calling any finder or init function, so
# neither __init__.py nor any C initialiser is ever reached.  numpy.core,
# linalg, and fft C extensions remain fully registered and work correctly.
#
# numpy.random.seed() / rand() / randn() are backed by Python's stdlib
# random.Random() so that the numpy_rng_isolation test passes: snapshot/restore
# resets the Random() instance state, so seeding with the same value always
# produces the same draw.

import random as _pyr

def _rand_stub(name, **attrs):
    """Create a stub module for a numpy.random C extension."""
    _m = _types.ModuleType(name)
    _m.__file__ = '<wasi-stub>'
    for _k, _v in attrs.items():
        setattr(_m, _k, _v)
    def _stub_getattr(_attr):
        # Let Python's normal "attribute not found" machinery handle dunder
        # lookups (e.g. __all__, __iter__, __getitem__).  Returning a callable
        # for __all__ would make "from stub import *" try to iterate over a
        # function and raise "'function' object is not iterable".
        if _attr.startswith('__'):
            raise AttributeError(_attr)
        def _not_impl(*_a, **_kw):
            raise NotImplementedError(
                'numpy.random C extensions are not available in the WASI '
                'sandbox (stubbed to prevent abort/unreachable traps). '
                'Array ops (np.array, np.allclose, np.linalg, np.fft) '
                'work fine; use pre-computed values instead of RNG.')
        _not_impl.__name__ = _attr
        return _not_impl
    _m.__getattr__ = _stub_getattr
    _sys.modules.setdefault(name, _m)
    return _m

class _StubBitGen:
    """Pure-Python stand-in for BitGenerator cdef base class."""
    def __init__(self, seed=None): pass

class _StubSeedSeq:
    """Pure-Python stand-in for SeedSequence."""
    def __init__(self, entropy=None, **kw): self.entropy = entropy

class _StubRandomState:
    """Pure-Python stand-in for RandomState (legacy mtrand API)."""
    def __init__(self, seed=None): pass

# Separate Random instance — its state is captured in the snapshot and reset
# on every restore, so seeding with the same value always yields the same draw.
_np_rng = _pyr.Random()

def _np_seed(seed=None, _rng=_np_rng):
    # _rng captured as default arg so del _np_rng below does not break this.
    _rng.seed(seed)

def _np_shape(args):
    if len(args) == 1 and isinstance(args[0], (tuple, list)):
        return tuple(int(x) for x in args[0])
    return tuple(int(x) for x in args)

def _np_fill(shape, scalar_fn):
    if not shape:
        return scalar_fn()
    if len(shape) == 1:
        return [scalar_fn() for _ in range(shape[0])]
    return [_np_fill(shape[1:], scalar_fn) for _ in range(shape[0])]

def _np_rand(*shape, _rng=_np_rng):
    """Return a scalar or nested list of uniform [0,1) floats matching shape."""
    shape = _np_shape(shape)
    return _np_fill(shape, _rng.random)

def _np_randn(*shape, _rng=_np_rng):
    """Return a scalar or nested list of standard-normal floats matching shape."""
    shape = _np_shape(shape)
    return _np_fill(shape, lambda: _rng.gauss(0.0, 1.0))

def _np_random_sample(size=None, _rng=_np_rng):
    shape = () if size is None else _np_shape((size,))
    return _np_fill(shape, _rng.random)

def _np_uniform(low=0.0, high=1.0, size=None, _rng=_np_rng):
    lo = float(low)
    hi = float(high)
    shape = () if size is None else _np_shape((size,))
    return _np_fill(shape, lambda: lo + (hi - lo) * _rng.random())

def _np_normal(loc=0.0, scale=1.0, size=None, _rng=_np_rng):
    mu = float(loc)
    sigma = float(scale)
    shape = () if size is None else _np_shape((size,))
    return _np_fill(shape, lambda: _rng.gauss(mu, sigma))

def _np_randint(low, high=None, size=None, dtype=int, _rng=_np_rng):
    if high is None:
        lo, hi = 0, int(low)
    else:
        lo, hi = int(low), int(high)
    shape = () if size is None else _np_shape((size,))
    return _np_fill(shape, lambda: dtype(_rng.randrange(lo, hi)))

def _np_choice(a, size=None, replace=True, p=None, _rng=_np_rng):
    if p is not None:
        raise NotImplementedError('numpy.random.choice polyfill does not support probability weights')
    seq = list(range(int(a))) if isinstance(a, int) else list(a)
    if not seq:
        raise ValueError('a cannot be empty')
    shape = () if size is None else _np_shape((size,))
    if not replace and shape:
        total = 1
        for dim in shape:
            total *= dim
        if total > len(seq):
            raise ValueError('Cannot take a larger sample than population when replace=False')
        shuffled = list(seq)
        _rng.shuffle(shuffled)
        idx = {'i': 0}
        def take():
            val = shuffled[idx['i']]
            idx['i'] += 1
            return val
        return _np_fill(shape, take)
    return _np_fill(shape, lambda: seq[_rng.randrange(0, len(seq))])

# ── numpy.random PACKAGE stub ─────────────────────────────────────────────────
# Must be registered FIRST so that "import numpy" finds numpy.random already in
# sys.modules and never executes numpy/random/__init__.py.
_np_rng_mod = _rand_stub('numpy.random',
    seed=_np_seed,
    rand=_np_rand,
    randn=_np_randn,
    random=_np_random_sample,
    random_sample=_np_random_sample,
    sample=_np_random_sample,
    ranf=_np_random_sample,
    uniform=_np_uniform,
    normal=_np_normal,
    randint=_np_randint,
    choice=_np_choice,
    RandomState=_StubRandomState,
    Generator=type('Generator', (), {}),
    BitGenerator=_StubBitGen,
    SeedSequence=_StubSeedSeq,
    MT19937=type('MT19937', (_StubBitGen,), {}),
    PCG64=type('PCG64', (_StubBitGen,), {}),
    PCG64DXSM=type('PCG64DXSM', (_StubBitGen,), {}),
    SFC64=type('SFC64', (_StubBitGen,), {}),
    Philox=type('Philox', (_StubBitGen,), {}),
)
# Mark as a package so Python does not try to locate it on disk.
_np_rng_mod.__path__ = []
_np_rng_mod.__package__ = 'numpy.random'
del _np_rng_mod

# ── numpy.random submodule stubs ──────────────────────────────────────────────
_rand_stub('numpy.random.bit_generator',
    BitGenerator=_StubBitGen,
    SeedSequence=_StubSeedSeq,
    ISeedSequence=_StubSeedSeq)

_rand_stub('numpy.random._mt19937',
    MT19937=type('MT19937', (_StubBitGen,), {}))

_rand_stub('numpy.random._philox',
    Philox=type('Philox', (_StubBitGen,), {}))

_rand_stub('numpy.random._pcg64',
    PCG64=type('PCG64', (_StubBitGen,), {}),
    PCG64DXSM=type('PCG64DXSM', (_StubBitGen,), {}))

_rand_stub('numpy.random._sfc64',
    SFC64=type('SFC64', (_StubBitGen,), {}))

_rand_stub('numpy.random._generator',
    Generator=type('Generator', (), {}))

_rand_stub('numpy.random._common')
_rand_stub('numpy.random._bounded_integers')

_rand_stub('numpy.random.mtrand',
    RandomState=_StubRandomState)

del _rand_stub, _StubBitGen, _StubSeedSeq, _StubRandomState
del _np_seed, _np_rand, _np_randn, _np_random_sample

# ── 5. Update _base_modules to include all stubs ─────────────────────────────
# Must happen here, before _handle_request's eviction finally-block runs.
# Every module added to sys.modules in sections 3-4 must be in _base_modules
# or it will be deleted by the eviction pass before the snapshot is taken.
_main._base_modules = frozenset(_sys.modules.keys())

def evaluation_function(r, a, p=None):
    # M-6 fix: return an error instead of a false positive if a request
    # arrives before phase 2 overwrites this stub.
    return {'error': 'phase1:stubs-ready — evaluation not yet available'}
`

// initSysPathPhase2 pre-warms numpy by importing it so the snapshot captures
// all numpy modules in sys.modules.  If this crashes (WASM unreachable) the
// error is reported as a non-fatal warning; phase 1 stubs remain intact and
// numpy is still importable per-request from the WASM binary.
const initSysPathPhase2 = `
import sys as _sys
import __main__ as _main

# ── Pre-warm numpy ────────────────────────────────────────────────────────────
# numpy.random and its submodules are already stubbed in sys.modules (phase 1).
# This import completes the rest of numpy's initialisation (core, linalg, fft).
try:
    import numpy as _np_pw
    _status = 'numpy ' + _np_pw.__version__ + ' pre-warmed'
    del _np_pw
except Exception as _exc:
    _status = 'numpy pre-warm exception: ' + str(_exc)
    del _exc

# Extend _base_modules to include all numpy.* modules so eviction never removes
# them.  From this point every request starts with numpy already imported.
_main._base_modules = frozenset(_sys.modules.keys())

def evaluation_function(r, a, p=None):
    return {'is_correct': True, 'feedback': _status}
`

func (r *ReactorPythonRunner) initSysPath(ctx context.Context) error {
	// Phase 1: sys.path, finder, stubs.  Failure here is fatal.
	phase1 := initSysPathPhase1
	if len(r.cfg.AllowedPaths) > 0 {
		var extra strings.Builder
		for _, p := range r.cfg.AllowedPaths {
			fmt.Fprintf(&extra, "\nif %q not in _sys.path: _sys.path.insert(0, %q)", p, p)
		}
		phase1 = strings.Replace(phase1,
			"# ── 2. Install a scandir-free meta path finder",
			extra.String()+"\n# ── 2. Install a scandir-free meta path finder",
			1)
	}
	if err := r.runInitScript(ctx, "phase1:setup", phase1); err != nil {
		return fmt.Errorf("initSysPath phase1: %w", err)
	}

	// Phase 2: numpy pre-warm.  Failure is non-fatal — numpy is still importable
	// per-request from the WASM binary; it just won't be cached in the snapshot.
	if err := r.runInitScript(ctx, "phase2:numpy-prewarm", initSysPathPhase2); err != nil {
		r.log.Warn("numpy pre-warm failed — numpy will be imported per-request",
			zap.Error(err))
	}
	return nil
}

// runInitScript runs a single-phase init script through py_exec and logs the result.
// It is a lower-level helper for initSysPath; callers decide whether errors are fatal.
func (r *ReactorPythonRunner) runInitScript(ctx context.Context, phase, script string) error {
	reqObj := map[string]any{
		"script": script,
		"method": "eval",
		"input":  json.RawMessage(`{"response":"","answer":""}`),
	}
	reqBytes, err := json.Marshal(reqObj)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	allocRes, err := r.fnAlloc.Call(ctx, uint64(len(reqBytes)))
	if err != nil {
		return fmt.Errorf("alloc: %w", err)
	}
	ptr := uint32(allocRes[0])
	if ptr == 0 {
		return fmt.Errorf("alloc returned NULL")
	}
	if !r.mod.Memory().Write(ptr, reqBytes) {
		return fmt.Errorf("write memory")
	}
	if _, err := r.fnPyExec.Call(ctx, uint64(ptr), uint64(len(reqBytes))); err != nil {
		return fmt.Errorf("py_exec: %w\nstderr: %s", err, r.stderrBuf.String())
	}
	bufPtrRes, _ := r.fnRespBuf.Call(ctx)
	lenPtrRes, _ := r.fnRespLen.Call(ctx)
	if lenRaw, ok := r.mod.Memory().Read(uint32(lenPtrRes[0]), 4); ok {
		respLen := int32(binary.LittleEndian.Uint32(lenRaw))
		if respLen > 0 {
			if body, ok := r.mod.Memory().Read(uint32(bufPtrRes[0]), uint32(respLen)); ok {
				r.log.Info("initSysPath "+phase, zap.String("result", string(body)))
				// Treat a Python-level error during init as a hard failure.
				var parsed map[string]any
				if json.Unmarshal(body, &parsed) == nil {
					if errMsg, ok := parsed["error"].(string); ok {
						return fmt.Errorf("python error: %s", errMsg)
					}
				}
			}
		}
	}
	return nil
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

// newSnapshotStrategy delegates to the shared selectSnapshotStrategy factory.
// The factory handles fallback to FullMemcpyStrategy internally.
func (r *ReactorPythonRunner) newSnapshotStrategy(mem api.Memory) SnapshotStrategy {
	strategy, err := NewSnapshotStrategy(r.cfg.SnapshotStrategy)
	if err != nil {
		r.log.Warn("invalid reactor-python snapshot strategy; falling back to full", zap.String("strategy", r.cfg.SnapshotStrategy), zap.Error(err))
		strategy, _ = NewSnapshotStrategy(SnapshotStrategyFull)
	}
	if err := strategy.Take(mem); err != nil {
		r.log.Warn("failed to take reactor-python memory snapshot", zap.Error(err))
	}
	return strategy
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

	// wasmBytes holds the python-reactor.wasm file contents, loaded once in
	// Start and shared by every pool runner (and replacement spawn) so the
	// 242 MB binary is not re-read from disk per runner.
	wasmBytes []byte

	// mu protects closed and serialises the closed/push transitions so a
	// replacement runner cannot land in the pool after Shutdown has begun.
	mu     sync.Mutex
	closed bool
	// closedCh is closed atomically with closed=true (under mu) by Shutdown.
	// Send selects on it to unblock a pool acquire racing Shutdown.
	closedCh chan struct{}
	// pending tracks BOTH in-flight Sends (Add in tryBeginSend, Done via
	// Send's defer) AND background goroutines spawned during a Send
	// (replacement spawns, discard-shutdowns). Shutdown waits on it before
	// draining the pool.
	//
	// Invariant: every pending.Add is either (a) under d.mu after observing
	// !closed, or (b) by code that already holds a pending count (e.g.
	// discardAsync inside Send). That keeps Add from racing Shutdown's Wait.
	pending sync.WaitGroup
}

// NewReactorPythonDispatcher creates a ReactorPythonDispatcher. Call Start first.
func NewReactorPythonDispatcher(cfg Config, log *zap.Logger) *ReactorPythonDispatcher {
	return &ReactorPythonDispatcher{
		cfg:      cfg,
		log:      log.Named("dispatcher_reactor_python"),
		closedCh: make(chan struct{}),
	}
}

// tryBeginSend atomically checks closed and increments pending. Returns false
// if Shutdown has begun; on true the caller MUST call pending.Done exactly
// once. See Dispatcher.tryBeginSend for the rationale.
func (d *ReactorPythonDispatcher) tryBeginSend() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false
	}
	d.pending.Add(1)
	return true
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

	if d.cfg.ModulePath == "" {
		return fmt.Errorf("reactor-python: ModulePath must be set (FUNCTION_WASM_MODULE)")
	}
	wasmBytes, err := os.ReadFile(d.cfg.ModulePath)
	if err != nil {
		return fmt.Errorf("reactor-python: read wasm %q: %w", d.cfg.ModulePath, err)
	}
	d.wasmBytes = wasmBytes
	d.log.Info("loaded python-reactor.wasm",
		zap.String("path", d.cfg.ModulePath),
		zap.Int("size", len(wasmBytes)),
	)

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
			runner := newReactorPythonRunnerWithBytes(d.cfg.ModulePath, d.wasmBytes, d.cfg, d.log)
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

	if !d.tryBeginSend() {
		return nil, fmt.Errorf("reactor-python: dispatcher is shut down")
	}
	defer d.pending.Done()

	var runner *ReactorPythonRunner
	select {
	case runner = <-d.pool:
	case <-d.closedCh:
		return nil, fmt.Errorf("reactor-python: dispatcher is shut down")
	case <-ctx.Done():
		return nil, fmt.Errorf("reactor-python: acquire runner: %w", ctx.Err())
	}
	defer func() {
		if runner.IsHealthy() {
			d.returnOrDiscard(runner)
		} else {
			// Runner's WASM module was closed (e.g. by a timeout). Discard it
			// and spin up a replacement in the background so pool capacity is
			// restored without blocking the current request.
			d.log.Warn("reactor runner unhealthy after request — dropping from pool, spawning replacement")
			d.discardAsync(runner)
			d.spawnReplacementAsync()
		}
	}()

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

// returnOrDiscard returns a healthy runner to the pool unless Shutdown has
// begun, in which case the runner is closed asynchronously so it does not leak
// past a drained pool.
//
// Must be called from inside Send (which holds a pending count) so that the
// Add issued by discardAsync cannot race Shutdown's pending.Wait.
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

// discardAsync closes a discarded runner in the background, tracked via the
// pending WaitGroup so Shutdown can wait for it before returning.
//
// Must be called from a goroutine that already holds a pending count (Send,
// via tryBeginSend). That invariant keeps Shutdown.pending.Wait blocked
// across this Add, eliminating the Add-after-Wait race.
func (d *ReactorPythonDispatcher) discardAsync(runner *ReactorPythonRunner) {
	d.pending.Add(1)
	go func() {
		defer d.pending.Done()
		_ = runner.Shutdown(context.Background())
	}()
}

// spawnReplacementAsync schedules spawnReplacement unless Shutdown has begun.
// Tracked via the pending WaitGroup.
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

// spawnReplacement initialises a fresh ReactorPythonRunner and adds it to the
// pool. Called in a goroutine when an unhealthy runner is discarded, so that
// pool capacity is eventually restored. Failures are logged but not fatal.
//
// If Shutdown begins while Init is running, the freshly initialised runner is
// closed immediately rather than inserted into the drained pool.
func (d *ReactorPythonDispatcher) spawnReplacement() {
	defer d.pending.Done()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	d.log.Info("reactor-python: initialising replacement runner")
	runner := newReactorPythonRunnerWithBytes(d.cfg.ModulePath, d.wasmBytes, d.cfg, d.log)
	if err := runner.Init(ctx); err != nil {
		d.log.Error("reactor-python: replacement runner init failed", zap.Error(err))
		return
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		d.log.Info("reactor-python: replacement runner born during shutdown — closing immediately")
		_ = runner.Shutdown(context.Background())
		return
	}
	d.pool <- runner
	d.mu.Unlock()
	d.log.Info("reactor-python: replacement runner ready")
}

// Shutdown drains the pool and shuts down each runner. Idempotent.
func (d *ReactorPythonDispatcher) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	// Close closedCh under mu so tryBeginSend's check + Add is atomic with the
	// shutdown signal.
	close(d.closedCh)
	d.mu.Unlock()

	d.log.Debug("shutting down reactor-python dispatcher")

	// Wait for in-flight Sends AND background goroutines (replacement spawns,
	// discard-shutdowns) to finish before draining the pool. Otherwise a Send
	// could still be running inside a runner while we tear that runner down,
	// or a late spawnReplacement could push to a drained pool.
	d.pending.Wait()

	// Non-blocking drain: after pending.Wait, no spawn or returnOrDiscard
	// goroutine will push to the pool, so we just close everything currently
	// buffered. (drainPool's blocking-for-cap-items semantics would deadlock
	// here when spawnReplacement took the closed-shortcut and never pushed.)
	var firstErr error
	for {
		select {
		case runner := <-d.pool:
			if err := runner.Shutdown(ctx); err != nil {
				d.log.Warn("error shutting down pooled runner", zap.Error(err))
				if firstErr == nil {
					firstErr = err
				}
			}
		default:
			return firstErr
		}
	}
}

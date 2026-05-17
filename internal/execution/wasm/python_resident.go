//go:build linux

package wasm

// ResidentPythonRunner keeps a Python interpreter running as a long-lived
// WASM module instance, communicating via stdin/stdout pipes.
//
// # Architecture
//
// Unlike PythonRunner (which re-instantiates python.wasm per request),
// ResidentPythonRunner runs python.wasm ONCE with a persistent server-loop
// script that reads JSON requests from stdin and writes JSON responses to
// stdout, looping forever.
//
// Python's stdin and stdout are connected to Go io.Pipe pairs. The host
// writes requests to Python's stdin pipe and reads responses from Python's
// stdout pipe. Since WASM execution is single-threaded in wazero but the
// WASI fd_read/fd_write host functions are Go functions that can block, we
// run the WASM module in a dedicated goroutine and communicate via channels.
//
// # Memory Snapshot / Restore
//
// After Python signals __READY__ (just before blocking in readline() at the
// top of the server loop for the first time), the host takes a snapshot of
// WASM linear memory. After every request, when Python has written __DONE__
// and is about to block in readline() again, the host restores the snapshot.
//
// Why this is safe: the WASM goroutine is blocked inside Go's os.File.Read()
// (called by wazero's fd_read host function). At that moment:
//
//   - The goroutine's Go stack holds wazero's interpreter state (instruction
//     pointer, value stack) as Go local variables — these are NOT affected by
//     our memory write.
//   - The iov buffer address (where wazero will write the next stdin bytes)
//     was captured into wazero's Go stack frame before the goroutine blocked,
//     so our memory restore does not change it.
//   - The CPython C-stack depth at each readline() block is identical (same
//     call path through the server loop every time), so the restored C stack
//     is consistent with the goroutine's saved interpreter state.
//
// The net effect: the next request executes against the pristine post-init
// CPython heap — sys.modules, global variables, and all other interpreter
// state are reset between requests.
//
// # Performance
//
// Initialization cost: ~7-8 s (Python startup + server loop import)
// Per-request cost: ~10-50 ms (script execution) + ~3-5 ms (memory restore)
//
// # Wire format
//
// Request (one JSON line on stdin):
//
//	{"script": "<python_source>", "method": "eval|preview", "input": {...}}
//
// Response (two lines on stdout):
//
//	{"is_correct": true, ...}\n
//	__DONE__\n

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
	"go.uber.org/zap"
)

// residentServerScript is the Python server loop that runs inside the WASM
// module. Protocol:
//
//  1. On startup: writes "__READY__\n" to stdout, then blocks in readline().
//     The host takes a memory snapshot at this point.
//
//  2. Per request: reads one JSON line, executes the script, writes the JSON
//     result followed by "__DONE__\n". The host restores the memory snapshot
//     after seeing __DONE__.
const residentServerScript = `import sys, json

# Signal that CPython is initialised and the server loop is about to start.
# The host takes a memory snapshot after receiving this line.
sys.stdout.write("__READY__\n")
sys.stdout.flush()

while True:
    try:
        line = sys.stdin.readline()
    except Exception as e:
        sys.stderr.write(f"stdin error: {e}\n")
        break
    if not line:
        break
    line = line.strip()
    if not line:
        continue
    try:
        req = json.loads(line)
    except Exception as e:
        sys.stdout.write(json.dumps({"error": f"json parse error: {e}"}) + "\n__DONE__\n")
        sys.stdout.flush()
        continue
    script_src = req.get("script", "")
    input_data = req.get("input", {})
    method = req.get("method", "eval")
    try:
        ns = {}
        exec(compile(script_src, "<eval>", "exec"), ns)
        if method == "preview":
            fn = ns.get("preview_function") or ns.get("evaluation_function")
            if fn is None:
                result = {"error": "no preview_function or evaluation_function defined in script"}
            else:
                result = fn(input_data.get("response"), input_data.get("answer"), input_data.get("params", {}))
        else:
            fn = ns.get("evaluation_function")
            if fn is None:
                result = {"error": "no evaluation_function defined in script"}
            else:
                result = fn(input_data.get("response"), input_data.get("answer"), input_data.get("params", {}))
    except Exception as e:
        result = {"error": str(e)}
    sys.stdout.write(json.dumps(result) + "\n__DONE__\n")
    sys.stdout.flush()
`

// ResidentPythonRunner keeps a Python interpreter alive as a long-running
// WASM instance, communicating via stdin/stdout pipes.
//
// NOT goroutine-safe for SendRequest: only one request can be in-flight at a
// time. Wrap with a pool or mutex for concurrent use.
type ResidentPythonRunner struct {
	wasmPath string
	cfg      Config
	log      *zap.Logger

	// Lifecycle
	mu          sync.Mutex
	initialized bool
	closed      bool

	// WASM runtime — kept alive for the duration so we can access the module
	// from outside the WASM goroutine. Closed by Shutdown (not by the goroutine).
	rt      wazero.Runtime
	modName string // unique name for rt.Module() lookup

	// Snapshot state — set during Init after __READY__, used by SendRequest.
	wasmMod          api.Module // live module reference (for memory access)
	memSnapshot      []byte     // copy of linear memory at __READY__ time
	snapStackPointer uint64     // value of __stack_pointer global at snapshot time

	// Communication pipes
	stdinWriter   io.WriteCloser  // host writes requests here → Python reads stdin
	stdoutReader  io.ReadCloser   // host reads responses here ← Python writes stdout
	stdoutScanner *bufio.Scanner  // line-by-line reader

	// Background WASM goroutine error tracking.
	// runErr is set atomically when the WASM goroutine exits; exitCh is closed.
	runErrVal atomic.Pointer[error]
	exitCh    chan struct{}
	cancel    context.CancelFunc
}

// NewResidentPythonRunner creates a ResidentPythonRunner.
// Call Init before SendRequest.
func NewResidentPythonRunner(wasmPath string, cfg Config, log *zap.Logger) *ResidentPythonRunner {
	cfg.applyDefaults()
	return &ResidentPythonRunner{
		wasmPath: wasmPath,
		cfg:      cfg,
		log:      log.Named("resident_python"),
		exitCh:   make(chan struct{}),
	}
}

// Init compiles python.wasm, instantiates the server-loop module, waits for
// Python to signal __READY__, then takes a linear-memory snapshot.
//
// This is the expensive step (~7-8 s). After Init returns, SendRequest calls
// are fast and each begins from a clean interpreter state.
func (r *ResidentPythonRunner) Init(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.initialized {
		return nil
	}

	if r.wasmPath == "" {
		return fmt.Errorf("resident python: wasmPath must be set")
	}

	r.log.Info("reading python.wasm", zap.String("path", r.wasmPath))
	wasmBytes, err := os.ReadFile(r.wasmPath)
	if err != nil {
		return fmt.Errorf("resident python: read %q: %w", r.wasmPath, err)
	}

	// Create stdin/stdout pipes.
	stdinR, stdinW := io.Pipe()   // Python reads from stdinR; host writes to stdinW
	stdoutR, stdoutW := io.Pipe() // Python writes to stdoutW; host reads from stdoutR

	r.stdinWriter = stdinW
	r.stdoutReader = stdoutR
	r.stdoutScanner = bufio.NewScanner(stdoutR)

	// Create runtime — stored in struct so Shutdown can close it.
	rtCfg := wazero.NewRuntimeConfig()
	if r.cfg.MaxMemoryPages > 0 {
		rtCfg = rtCfg.WithMemoryLimitPages(r.cfg.MaxMemoryPages)
	}
	r.rt = wazero.NewRuntimeWithConfig(ctx, rtCfg)

	// Compile module.
	r.log.Info("compiling python.wasm (this takes ~1-2 s)")
	compiled, err := r.rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = r.rt.Close(ctx)
		r.rt = nil
		return fmt.Errorf("resident python: compile: %w", err)
	}

	// Install WASI.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r.rt); err != nil {
		_ = compiled.Close(ctx)
		_ = r.rt.Close(ctx)
		r.rt = nil
		return fmt.Errorf("resident python: instantiate wasi: %w", err)
	}

	// Give the module a unique name so we can retrieve it via rt.Module().
	r.modName = fmt.Sprintf("python-resident-%d", time.Now().UnixNano())

	var stderrBuf bytes.Buffer
	mc := wazero.NewModuleConfig().
		WithArgs("python3", "-c", residentServerScript).
		WithName(r.modName).
		WithStdin(stdinR).
		WithStdout(stdoutW).
		WithStderr(&stderrBuf).
		WithSysNanosleep().
		WithSysWalltime().
		WithSysNanotime()

	// Run the WASM module in a background goroutine.
	runCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	r.log.Info("starting Python server loop (background goroutine)")
	go func() {
		defer func() {
			_ = stdoutW.Close()
			_ = stdinR.Close()
			_ = compiled.Close(context.Background())
			// Note: r.rt is NOT closed here; Shutdown() is responsible.
			close(r.exitCh)
		}()

		_, runErr := r.rt.InstantiateModule(runCtx, compiled, mc)
		if runErr != nil {
			if exitErr, ok := runErr.(*sys.ExitError); ok && exitErr.ExitCode() == 0 {
				return
			}
			r.log.Error("python WASM exited with error",
				zap.Error(runErr),
				zap.String("stderr", stderrBuf.String()),
			)
			r.runErrVal.Store(&runErr)
		}
	}()

	// Wait for Python to emit __READY__ (indicates server loop is about to
	// block in readline() for the first time).
	r.log.Info("waiting for Python to signal __READY__ (~7-8 s)...")

	readyCtx, readyCancel := context.WithTimeout(ctx, 120*time.Second)
	defer readyCancel()

	readyLine, err := r.scanLineWithContext(readyCtx)
	if err != nil {
		_ = r.cleanup()
		return fmt.Errorf("resident python: waiting for __READY__: %w", err)
	}
	if readyLine != "__READY__" {
		_ = r.cleanup()
		return fmt.Errorf("resident python: expected __READY__, got %q", readyLine)
	}
	r.log.Info("Python signalled __READY__")

	// At this point Python has written __READY__ and is executing the next
	// statement (sys.stdin.readline()). Give the WASM goroutine time to enter
	// os.File.Read() before we take the snapshot.
	runtime.Gosched()
	time.Sleep(10 * time.Millisecond)

	// Retrieve the live module from the runtime.
	r.wasmMod = r.rt.Module(r.modName)
	if r.wasmMod == nil {
		_ = r.cleanup()
		return fmt.Errorf("resident python: module %q not found in runtime after __READY__", r.modName)
	}

	// Take the initial memory snapshot.
	if err := r.takeSnapshot(); err != nil {
		_ = r.cleanup()
		return fmt.Errorf("resident python: initial snapshot: %w", err)
	}

	memMB := float64(len(r.memSnapshot)) / (1024 * 1024)
	r.log.Info("memory snapshot taken",
		zap.Float64("snapshot_mb", memMB),
	)

	r.initialized = true
	r.log.Info("ResidentPythonRunner initialized successfully")
	return nil
}

// SendRequest sends a script + method + input to the resident Python
// interpreter. After receiving the response, it restores the memory snapshot
// so the next request begins from a clean state.
func (r *ResidentPythonRunner) SendRequest(ctx context.Context, script, method, inputJSON string) (map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.initialized || r.closed {
		return nil, fmt.Errorf("resident python: not initialized — call Init first")
	}

	reqTimeout := r.cfg.Timeout
	if reqTimeout == 0 {
		reqTimeout = 30 * time.Second
	}

	if method == "" {
		method = "eval"
	}
	reqObj := map[string]any{
		"script": script,
		"method": method,
		"input":  json.RawMessage(inputJSON),
	}
	reqBytes, err := json.Marshal(reqObj)
	if err != nil {
		return nil, fmt.Errorf("resident python: marshal request: %w", err)
	}
	reqLine := string(reqBytes) + "\n"

	r.log.Debug("SendRequest: writing to stdin", zap.Int("len", len(reqLine)))

	// Write request to Python's stdin.
	writeDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(r.stdinWriter, reqLine)
		writeDone <- err
	}()

	writeCtx, writeCancel := context.WithTimeout(ctx, reqTimeout)
	defer writeCancel()

	select {
	case werr := <-writeDone:
		if werr != nil {
			return nil, fmt.Errorf("resident python: write request: %w", werr)
		}
	case <-writeCtx.Done():
		return nil, fmt.Errorf("resident python: context cancelled during write: %w", ctx.Err())
	case <-r.exitCh:
		return nil, r.wrapRunErr("WASM exited during request write")
	}

	// Read the JSON response line.
	r.log.Debug("SendRequest: waiting for response")
	respCtx, respCancel := context.WithTimeout(ctx, reqTimeout)
	defer respCancel()

	jsonLine, err := r.scanLineWithContext(respCtx)
	if err != nil {
		return nil, fmt.Errorf("resident python: read response: %w", err)
	}
	r.log.Debug("SendRequest: got response", zap.String("line", jsonLine))

	// Read the __DONE__ sentinel.
	doneCtx, doneCancel := context.WithTimeout(ctx, 5*time.Second)
	defer doneCancel()

	doneLine, err := r.scanLineWithContext(doneCtx)
	if err != nil {
		return nil, fmt.Errorf("resident python: read __DONE__: %w", err)
	}
	if doneLine != "__DONE__" {
		return nil, fmt.Errorf("resident python: expected __DONE__, got %q", doneLine)
	}

	// Python has written __DONE__ and is calling readline() again.
	// Give it time to enter os.File.Read() before we restore memory.
	runtime.Gosched()
	time.Sleep(2 * time.Millisecond)

	// Restore memory snapshot — resets CPython heap to post-init state.
	if err := r.restoreSnapshot(); err != nil {
		r.log.Error("failed to restore memory snapshot", zap.Error(err))
		// Non-fatal for this request but runner state is now dirty.
	}

	// Parse and return result.
	result, err := parseJSONResponse(jsonLine)
	if err != nil {
		return nil, fmt.Errorf("resident python: parse response: %w; raw: %.200s", err, jsonLine)
	}
	if errMsg, ok := result["error"].(string); ok {
		return nil, fmt.Errorf("resident python script error: %s", errMsg)
	}
	return result, nil
}

// takeSnapshot captures WASM linear memory into r.memSnapshot.
// Also saves mutable exported globals (e.g. __stack_pointer).
// Must be called with r.mu held.
func (r *ResidentPythonRunner) takeSnapshot() error {
	mem := r.wasmMod.Memory()
	if mem == nil {
		return nil
	}
	size := mem.Size()
	if size == 0 {
		return nil
	}
	buf, ok := mem.Read(0, size)
	if !ok {
		return fmt.Errorf("snapshot: could not read %d bytes of linear memory", size)
	}
	r.memSnapshot = make([]byte, len(buf))
	copy(r.memSnapshot, buf)

	// Save __stack_pointer so restoreSnapshot can reset it explicitly.
	if sp := r.wasmMod.ExportedGlobal("__stack_pointer"); sp != nil {
		r.snapStackPointer = sp.Get()
	}
	return nil
}

// restoreSnapshot writes r.memSnapshot back into WASM linear memory.
// Must be called with r.mu held and only when the WASM goroutine is blocked
// in os.File.Read() (i.e., Python is waiting in readline()).
func (r *ResidentPythonRunner) restoreSnapshot() error {
	if r.memSnapshot == nil || r.wasmMod == nil {
		return nil
	}
	mem := r.wasmMod.Memory()
	if mem == nil {
		return nil
	}
	if !mem.Write(0, r.memSnapshot) {
		return fmt.Errorf("snapshot: failed to restore %d bytes of linear memory", len(r.memSnapshot))
	}

	// Restore __stack_pointer if it is a mutable exported global.
	// The stack pointer must be at its snapshot value for the CPython C stack
	// to be consistent with the restored heap.
	if sp := r.wasmMod.ExportedGlobal("__stack_pointer"); sp != nil {
		if mg, ok := sp.(api.MutableGlobal); ok {
			// Re-read the snapshot value from the saved memory.
			// (We snapshot globals implicitly: at __READY__ time Python's
			// C stack is at the same depth as after each __DONE__, so the
			// __stack_pointer value should be identical — but we restore it
			// explicitly for correctness.)
			mg.Set(r.snapStackPointer)
		}
	}
	return nil
}

// scanLineWithContext reads one line from stdoutScanner, aborting if the
// context expires or the WASM goroutine exits.
func (r *ResidentPythonRunner) scanLineWithContext(ctx context.Context) (string, error) {
	type scanResult struct {
		line string
		err  error
	}
	ch := make(chan scanResult, 1)
	go func() {
		if r.stdoutScanner.Scan() {
			ch <- scanResult{line: r.stdoutScanner.Text()}
		} else {
			err := r.stdoutScanner.Err()
			if err == nil {
				err = io.EOF
			}
			ch <- scanResult{err: err}
		}
	}()

	select {
	case res := <-ch:
		return res.line, res.err
	case <-ctx.Done():
		return "", fmt.Errorf("context: %w", ctx.Err())
	case <-r.exitCh:
		return "", r.wrapRunErr("WASM goroutine exited")
	}
}

// wrapRunErr constructs an error from the stored WASM exit error (if any).
func (r *ResidentPythonRunner) wrapRunErr(msg string) error {
	if p := r.runErrVal.Load(); p != nil && *p != nil {
		return fmt.Errorf("resident python: %s: %w", msg, *p)
	}
	return fmt.Errorf("resident python: %s", msg)
}

// cleanup closes all pipes and cancels the background goroutine.
// Must be called with r.mu held.
func (r *ResidentPythonRunner) cleanup() error {
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	if r.stdinWriter != nil {
		_ = r.stdinWriter.Close()
		r.stdinWriter = nil
	}
	if r.stdoutReader != nil {
		_ = r.stdoutReader.Close()
		r.stdoutReader = nil
	}
	r.initialized = false
	r.closed = true
	return nil
}

// Shutdown closes stdin (causing Python to see EOF and exit cleanly),
// waits for the WASM goroutine to finish, then closes the runtime.
func (r *ResidentPythonRunner) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.log.Debug("shutting down resident python runner")

	if r.closed {
		return nil
	}

	// Close stdin → Python sees EOF → server loop exits.
	if r.stdinWriter != nil {
		_ = r.stdinWriter.Close()
		r.stdinWriter = nil
	}

	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}

	// Wait for WASM goroutine to exit.
	select {
	case <-r.exitCh:
	case <-ctx.Done():
		r.log.Warn("timeout waiting for WASM goroutine to finish")
	case <-time.After(10 * time.Second):
		r.log.Warn("timeout waiting for WASM goroutine to finish")
	}

	if r.stdoutReader != nil {
		_ = r.stdoutReader.Close()
		r.stdoutReader = nil
	}

	// Close the runtime now that the module has exited.
	if r.rt != nil {
		if err := r.rt.Close(ctx); err != nil {
			r.log.Warn("error closing wazero runtime", zap.Error(err))
		}
		r.rt = nil
	}

	r.initialized = false
	r.closed = true
	return nil
}

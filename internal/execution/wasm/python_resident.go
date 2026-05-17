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
// # Isolation
//
// Each request is executed inside exec(compile(script, "<eval>", "exec"), ns={})
// with a fresh empty namespace. This provides Python-level isolation: globals,
// local variables, and module-level side-effects from one request do not bleed
// into the next. The CPython interpreter and its imported standard library
// modules remain alive across requests (giving fast per-request execution).
//
// # Performance
//
// Initialization cost: ~7-8 s (Python startup + server loop import)
// Per-request cost: ~10-50 ms (script exec in fresh namespace)
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
	"sync"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
	"go.uber.org/zap"
)

// residentServerScript is the Python server loop that runs inside the WASM
// module. Protocol:
//
//  1. On startup: writes "__READY__\n" to stdout, signals that the interpreter
//     is initialised and the server loop is about to start.
//
//  2. Per request: reads one JSON line, executes the user script in a fresh
//     namespace (exec with ns={}), writes the JSON result followed by
//     "__DONE__\n".
const residentServerScript = `import sys, json

# Signal that CPython is initialised and the server loop is about to start.
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

	// Communication pipes
	stdinWriter  io.WriteCloser // host writes requests here → Python reads stdin
	stdoutReader io.ReadCloser  // host reads responses here ← Python writes stdout

	// linesCh is fed by a single background reader goroutine that owns
	// stdoutScanner. Using one goroutine eliminates the race/leak that occurs
	// when per-call goroutines outlive a cancelled context.
	linesCh chan string

	// healthy is false if the runner has entered an unrecoverable error state.
	// An unhealthy runner must not be returned to the pool.
	healthy atomic.Bool

	// Background WASM goroutine error tracking.
	// runErrVal is set atomically when the WASM goroutine exits; exitCh is closed.
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

// Init compiles python.wasm, instantiates the server-loop module, and waits
// for Python to signal __READY__.
//
// This is the expensive step (~7-8 s). After Init returns, SendRequest calls
// are fast — each executes in a fresh Python namespace (exec with ns={}).
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
	r.linesCh = make(chan string, 8)

	// Background reader goroutine: owns the Scanner for stdoutR's lifetime.
	// Exits when the pipe closes (Python exits or Shutdown closes stdoutReader).
	scanner := bufio.NewScanner(stdoutR)
	go func() {
		for scanner.Scan() {
			r.linesCh <- scanner.Text()
		}
		close(r.linesCh)
	}()

	// Create runtime — local variable; the WASM goroutine closes it on exit.
	rtCfg := wazero.NewRuntimeConfig()
	if r.cfg.MaxMemoryPages > 0 {
		rtCfg = rtCfg.WithMemoryLimitPages(r.cfg.MaxMemoryPages)
	}
	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)

	// Compile module.
	r.log.Info("compiling python.wasm (this takes ~1-2 s)")
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx)
		return fmt.Errorf("resident python: compile: %w", err)
	}

	// Install WASI.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = compiled.Close(ctx)
		_ = rt.Close(ctx)
		return fmt.Errorf("resident python: instantiate wasi: %w", err)
	}

	var stderrBuf bytes.Buffer
	mc := wazero.NewModuleConfig().
		WithArgs("python3", "-c", residentServerScript).
		WithName("").
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
			_ = rt.Close(context.Background())
			close(r.exitCh)
		}()

		_, runErr := rt.InstantiateModule(runCtx, compiled, mc)
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

	r.initialized = true
	r.healthy.Store(true)
	r.log.Info("ResidentPythonRunner initialized successfully")
	return nil
}

// IsHealthy returns false if the runner has entered an unrecoverable error
// state. An unhealthy runner should be shut down and not reused.
func (r *ResidentPythonRunner) IsHealthy() bool {
	return r.healthy.Load()
}

// SendRequest sends a script + method + input to the resident Python
// interpreter. The script is executed in a fresh namespace (exec with ns={})
// so globals do not bleed between requests.
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

// scanLineWithContext reads one line delivered by the background reader
// goroutine, aborting if the context expires or the WASM goroutine exits.
// No goroutines are spawned per call — the reader goroutine runs for the
// entire lifetime of the runner.
func (r *ResidentPythonRunner) scanLineWithContext(ctx context.Context) (string, error) {
	select {
	case line, ok := <-r.linesCh:
		if !ok {
			return "", io.EOF
		}
		return line, nil
	case <-ctx.Done():
		return "", fmt.Errorf("context: %w", ctx.Err())
	case <-r.exitCh:
		// Drain any buffered line that arrived before exit.
		select {
		case line, ok := <-r.linesCh:
			if ok {
				return line, nil
			}
		default:
		}
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

	// Wait for WASM goroutine to exit (it will close rt when done).
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

	r.initialized = false
	r.closed = true
	return nil
}

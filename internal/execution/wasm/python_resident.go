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
// # Performance
//
// Initialization cost: ~7-8 s (Python startup + server loop import)
// Per-request cost: ~10-50 ms (Python script execution only, no startup)
//
// This is a 5-15x speedup over PythonRunner (~163 ms per request), primarily
// because Python initialization (importing sys, json, cpython internals) is
// done once.
//
// # Script format
//
// The server loop expects one JSON request per line on stdin:
//
//	{"script": "<python_source>", "input": {...}}
//
// And writes one JSON response per line on stdout:
//
//	{"is_correct": true, "feedback": "..."}
//
// OR an error response:
//
//	{"error": "..."}

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
	"go.uber.org/zap"
)

// residentServerScript is the Python server loop that runs inside the WASM module.
// It reads one JSON request per line, executes the script, and writes the result.
const residentServerScript = `import sys, json

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
        sys.stdout.write(json.dumps({"error": f"json parse error: {e}"}) + "\n")
        sys.stdout.flush()
        continue
    script_src = req.get("script", "")
    input_data = req.get("input", {})
    try:
        ns = {}
        exec(compile(script_src, "<eval>", "exec"), ns)
        fn = ns.get("evaluation_function")
        if fn is None:
            result = {"error": "no evaluation_function defined in script"}
        else:
            result = fn(input_data.get("response"), input_data.get("answer"), input_data.get("params", {}))
    except Exception as e:
        result = {"error": str(e)}
    sys.stdout.write(json.dumps(result) + "\n")
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
	stdinWriter  io.WriteCloser  // host writes requests here → Python reads stdin
	stdoutReader io.ReadCloser   // host reads responses here ← Python writes stdout
	stdoutScanner *bufio.Scanner // line-by-line reader

	// Background WASM goroutine
	runErr  chan error // receives WASM exit error (or nil on clean exit)
	cancel  context.CancelFunc
}

// NewResidentPythonRunner creates a ResidentPythonRunner.
// Call Init before SendRequest.
func NewResidentPythonRunner(wasmPath string, cfg Config, log *zap.Logger) *ResidentPythonRunner {
	cfg.applyDefaults()
	return &ResidentPythonRunner{
		wasmPath: wasmPath,
		cfg:      cfg,
		log:      log.Named("resident_python"),
	}
}

// Init compiles python.wasm, instantiates the server-loop module, and waits
// for Python to finish initializing (indicated by the server loop being ready
// to accept requests).
//
// This is the expensive step (~7-8 s). After Init returns, SendRequest calls
// are fast (<50 ms each).
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
	// stdinR → Python stdin (WASM reads from this)
	// stdinW → host writes requests to this
	stdinR, stdinW := io.Pipe()
	// stdoutR → host reads responses from this
	// stdoutW → Python stdout (WASM writes to this)
	stdoutR, stdoutW := io.Pipe()

	r.stdinWriter = stdinW
	r.stdoutReader = stdoutR
	r.stdoutScanner = bufio.NewScanner(stdoutR)

	// Create runtime.
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

	// Build stderr capture for debugging.
	var stderrBuf bytes.Buffer

	mc := wazero.NewModuleConfig().
		WithArgs("python3", "-c", residentServerScript).
		WithStdin(stdinR).
		WithStdout(stdoutW).
		WithStderr(&stderrBuf).
		WithSysNanosleep().
		WithSysWalltime().
		WithSysNanotime().
		WithName("")

	// Run the WASM module in a background goroutine.
	runCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.runErr = make(chan error, 1)

	r.log.Info("starting Python server loop (background goroutine)")
	go func() {
		defer func() {
			_ = stdoutW.Close()
			_ = stdinR.Close()
			_ = compiled.Close(context.Background())
			_ = rt.Close(context.Background())
		}()

		_, runErr := rt.InstantiateModule(runCtx, compiled, mc)
		if runErr != nil {
			if exitErr, ok := runErr.(*sys.ExitError); ok && exitErr.ExitCode() == 0 {
				r.runErr <- nil
				return
			}
			r.log.Error("python WASM exited with error",
				zap.Error(runErr),
				zap.String("stderr", stderrBuf.String()),
			)
			r.runErr <- runErr
			return
		}
		r.runErr <- nil
	}()

	// Wait for Python to be ready. Python prints nothing during startup, so
	// we just wait a moment to let initialization complete. We use a "ping"
	// mechanism: send a dummy request and wait for a response.
	r.log.Info("waiting for Python to initialize...")

	// Try sending a no-op request to check readiness.
	readyCtx, readyCancel := context.WithTimeout(ctx, 120*time.Second)
	defer readyCancel()

	pingScript := `def evaluation_function(response, answer, params=None): return {"is_correct": True, "feedback": "ready"}`
	pingReq, _ := json.Marshal(map[string]any{
		"script": pingScript,
		"input":  map[string]string{"response": "x", "answer": "x"},
	})
	pingLine := string(pingReq) + "\n"

	// Write ping with a write deadline.
	writeDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(stdinW, pingLine)
		writeDone <- err
	}()

	select {
	case werr := <-writeDone:
		if werr != nil {
			_ = r.cleanup()
			return fmt.Errorf("resident python: write ping: %w", werr)
		}
	case <-readyCtx.Done():
		_ = r.cleanup()
		return fmt.Errorf("resident python: timeout waiting to send ping: %w", readyCtx.Err())
	case runErr := <-r.runErr:
		_ = r.cleanup()
		return fmt.Errorf("resident python: WASM exited during init: %v", runErr)
	}

	// Read ping response with a deadline.
	respDone := make(chan string, 1)
	respErr := make(chan error, 1)
	go func() {
		if r.stdoutScanner.Scan() {
			respDone <- r.stdoutScanner.Text()
		} else {
			if err := r.stdoutScanner.Err(); err != nil {
				respErr <- err
			} else {
				respErr <- io.EOF
			}
		}
	}()

	select {
	case line := <-respDone:
		r.log.Info("Python ready", zap.String("ping_response", line))
	case err := <-respErr:
		_ = r.cleanup()
		return fmt.Errorf("resident python: ping response error: %w", err)
	case <-readyCtx.Done():
		_ = r.cleanup()
		return fmt.Errorf("resident python: timeout waiting for ping response: %w", readyCtx.Err())
	case runErr := <-r.runErr:
		_ = r.cleanup()
		return fmt.Errorf("resident python: WASM exited during ping: %v", runErr)
	}

	r.initialized = true
	r.log.Info("ResidentPythonRunner initialized successfully")
	return nil
}

// SendRequest sends a script + input to the resident Python interpreter and
// returns the parsed JSON result.
//
// The request is:
//
//	{"script": "<python_source>", "input": <inputJSON>}
//
// The response is parsed as a map.
func (r *ResidentPythonRunner) SendRequest(ctx context.Context, script, inputJSON string) (map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.initialized || r.closed {
		return nil, fmt.Errorf("resident python: not initialized — call Init first")
	}

	// Apply per-request timeout.
	reqTimeout := r.cfg.Timeout
	if reqTimeout == 0 {
		reqTimeout = 30 * time.Second
	}

	// Build request JSON.
	reqObj := map[string]any{
		"script": script,
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

	select {
	case werr := <-writeDone:
		if werr != nil {
			return nil, fmt.Errorf("resident python: write request: %w", werr)
		}
	case <-ctx.Done():
		return nil, fmt.Errorf("resident python: context cancelled during write: %w", ctx.Err())
	case runErr := <-r.runErr:
		return nil, fmt.Errorf("resident python: WASM exited during request: %v", runErr)
	}

	// Read one response line from Python's stdout.
	r.log.Debug("SendRequest: waiting for response")
	respDone := make(chan string, 1)
	respErr := make(chan error, 1)
	go func() {
		if r.stdoutScanner.Scan() {
			respDone <- r.stdoutScanner.Text()
		} else {
			if err := r.stdoutScanner.Err(); err != nil {
				respErr <- err
			} else {
				respErr <- io.EOF
			}
		}
	}()

	deadline := time.After(reqTimeout)
	select {
	case line := <-respDone:
		r.log.Debug("SendRequest: got response", zap.String("line", line))
		result, err := parseJSONResponse(line)
		if err != nil {
			return nil, fmt.Errorf("resident python: parse response: %w; raw: %.200s", err, line)
		}
		// Check for Python-level errors.
		if errMsg, ok := result["error"].(string); ok {
			return nil, fmt.Errorf("resident python script error: %s", errMsg)
		}
		return result, nil

	case err := <-respErr:
		return nil, fmt.Errorf("resident python: read response: %w", err)

	case <-deadline:
		return nil, fmt.Errorf("resident python: request timed out after %v", reqTimeout)

	case <-ctx.Done():
		return nil, fmt.Errorf("resident python: context cancelled: %w", ctx.Err())

	case runErr := <-r.runErr:
		return nil, fmt.Errorf("resident python: WASM exited during request: %v", runErr)
	}
}

// cleanup closes all pipes and cancels the background goroutine.
// Must be called with mu held.
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

// Shutdown closes the stdin pipe (causing Python to see EOF and exit cleanly),
// waits for the WASM goroutine to finish, then closes the runtime.
func (r *ResidentPythonRunner) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.log.Debug("shutting down resident python runner")

	if r.closed {
		return nil
	}

	// Close stdin → Python sees EOF → server loop exits → proc_exit.
	if r.stdinWriter != nil {
		_ = r.stdinWriter.Close()
		r.stdinWriter = nil
	}

	// Cancel context to unblock any blocked WASM operations.
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}

	// Wait for WASM goroutine to finish.
	if r.runErr != nil {
		select {
		case <-r.runErr:
		case <-ctx.Done():
			r.log.Warn("timeout waiting for WASM goroutine to finish")
		case <-time.After(10 * time.Second):
			r.log.Warn("timeout waiting for WASM goroutine to finish")
		}
		r.runErr = nil
	}

	if r.stdoutReader != nil {
		_ = r.stdoutReader.Close()
		r.stdoutReader = nil
	}

	r.initialized = false
	r.closed = true
	return nil
}

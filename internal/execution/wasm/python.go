package wasm

// PythonRunner runs Python eval functions in a CPython-WASI sandbox.
//
// Unlike the reactor-based Dispatcher, Python uses per-request instantiation
// (command mode WASM). CompileModule is cached; only instantiation is
// per-request. This means:
//
//   - Compilation happens once in Start (~1-2 s for python.wasm).
//   - Each RunScript call instantiates a fresh module, runs python3 -c <script>
//     with the provided JSON on stdin, captures stdout, and closes the module.
//   - No shared mutable state across requests — true isolation.
//
// Sandbox guarantees (inherited from wazero defaults):
//   - No host filesystem access (no WithFSConfig).
//   - No host environment variables (no WithEnv).
//   - stdout/stderr captured into in-memory buffers.
//   - Memory bounded by MaxMemoryPages.
//   - CPU bounded by context deadline (Timeout).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
	"go.uber.org/zap"
)

// PythonRunner compiles python.wasm once and runs scripts per-request.
type PythonRunner struct {
	wasmPath string
	compiled wazero.CompiledModule
	rt       wazero.Runtime
	cfg      Config
	log      *zap.Logger
}

// NewPythonRunner creates a PythonRunner. Call Start before using RunScript.
func NewPythonRunner(wasmPath string, cfg Config, log *zap.Logger) *PythonRunner {
	cfg.applyDefaults()
	return &PythonRunner{
		wasmPath: wasmPath,
		cfg:      cfg,
		log:      log.Named("python_runner"),
	}
}

// Start reads and compiles python.wasm. This is the expensive step (~1-2 s).
// The compiled module is cached and reused for all subsequent RunScript calls.
func (r *PythonRunner) Start(ctx context.Context) error {
	if r.wasmPath == "" {
		return fmt.Errorf("python: wasmPath must be set")
	}

	r.log.Info("compiling python.wasm", zap.String("path", r.wasmPath))

	wasmBytes, err := os.ReadFile(r.wasmPath)
	if err != nil {
		return fmt.Errorf("python: read wasm file %q: %w", r.wasmPath, err)
	}

	rtCfg := wazero.NewRuntimeConfig()
	if r.cfg.MaxMemoryPages > 0 {
		rtCfg = rtCfg.WithMemoryLimitPages(r.cfg.MaxMemoryPages)
	}

	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)
	r.rt = rt

	// Install WASI host functions required by CPython-WASI.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		r.rt = nil
		return fmt.Errorf("python: instantiate wasi: %w", err)
	}

	// Compile once — this is the expensive step; instantiation is per-request.
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx)
		r.rt = nil
		return fmt.Errorf("python: compile module: %w", err)
	}

	r.compiled = compiled
	r.log.Info("python.wasm compiled successfully")
	return nil
}

// RunScript instantiates python.wasm fresh, runs:
//
//	python3 -c <script>
//
// with inputJSON provided on stdin. It captures stdout, parses it as JSON,
// and returns the result map.
//
// The module exits via proc_exit (WASI command mode), so a non-zero exit code
// is captured and returned as an error only when stdout is empty. If the
// Python script writes a valid JSON result to stdout and then exits non-zero,
// the JSON result is still returned (the script may call sys.exit(0) or not).
func (r *PythonRunner) RunScript(ctx context.Context, script string, inputJSON string) (map[string]any, error) {
	if r.compiled == nil {
		return nil, fmt.Errorf("python: runner not started")
	}

	// Apply per-request timeout.
	if r.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.cfg.Timeout)
		defer cancel()
	}

	var stdoutBuf, stderrBuf bytes.Buffer

	// Command-mode WASM: argv[0]=program name, then flags.
	// No filesystem, no env vars — pure stdio sandboxing.
	mc := wazero.NewModuleConfig().
		WithArgs("python3", "-c", script).
		WithStdin(strings.NewReader(inputJSON)).
		WithStdout(&stdoutBuf).
		WithStderr(&stderrBuf).
		WithSysNanosleep().
		WithSysWalltime().
		WithSysNanotime().
		// Each instantiation must have a unique name; use empty string to let
		// wazero auto-generate one.
		WithName("").
		// Command mode: run _start (not _initialize).
		WithStartFunctions("_start")

	r.log.Debug("running python script",
		zap.Int("script_len", len(script)),
		zap.Int("input_len", len(inputJSON)),
	)

	_, err := r.rt.InstantiateModule(ctx, r.compiled, mc)
	if err != nil {
		// CPython signals exit via proc_exit; wazero surfaces this as
		// *sys.ExitError.  A zero exit code is success; non-zero is failure.
		// In both cases we still try to parse stdout as JSON.
		if exitErr, ok := err.(*sys.ExitError); ok {
			exitCode := exitErr.ExitCode()
			stdout := stdoutBuf.String()
			stderr := stderrBuf.String()

			r.log.Debug("python exited",
				zap.Uint32("exit_code", exitCode),
				zap.Int("stdout_len", len(stdout)),
				zap.Int("stderr_len", len(stderr)),
			)

			if stdout != "" {
				result, parseErr := parseJSONResponse(stdout)
				if parseErr == nil {
					return result, nil
				}
				// stdout present but not valid JSON — fall through to error.
				return nil, fmt.Errorf("python: exit %d, stdout not valid JSON: %w; stderr: %s", exitCode, parseErr, stderr)
			}

			if exitCode != 0 {
				return nil, fmt.Errorf("python: exit %d; stderr: %s", exitCode, stderr)
			}

			// Exited 0 with no stdout — return empty map.
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("python: instantiate/run: %w", err)
	}

	// Module returned without calling proc_exit (unusual for CPython, but handle
	// it gracefully).
	stdout := stdoutBuf.String()
	if stdout == "" {
		return map[string]any{}, nil
	}

	result, err := parseJSONResponse(stdout)
	if err != nil {
		return nil, fmt.Errorf("python: parse stdout JSON: %w; stderr: %s", err, stderrBuf.String())
	}

	return result, nil
}

// Shutdown closes all compiled modules and the wazero runtime.
func (r *PythonRunner) Shutdown(ctx context.Context) error {
	r.log.Debug("shutting down python runner")

	if r.compiled != nil {
		if err := r.compiled.Close(ctx); err != nil {
			return fmt.Errorf("python: close compiled module: %w", err)
		}
		r.compiled = nil
	}

	if r.rt != nil {
		if err := r.rt.Close(ctx); err != nil {
			return fmt.Errorf("python: close runtime: %w", err)
		}
		r.rt = nil
	}

	return nil
}

// parseJSONResponse unmarshals a JSON object from the given string.
func parseJSONResponse(s string) (map[string]any, error) {
	s = strings.TrimSpace(s)
	var result map[string]any
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return nil, fmt.Errorf("unmarshal JSON: %w (raw: %.200s)", err, s)
	}
	return result, nil
}

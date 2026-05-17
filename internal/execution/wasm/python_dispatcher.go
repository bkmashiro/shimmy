//go:build linux

package wasm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sync"

	"go.uber.org/zap"
)

// PythonDispatcher implements dispatcher.Dispatcher for the resident CPython-WASI
// backend. It maintains a pool of ResidentPythonRunner instances, each keeping a
// python.wasm interpreter alive across requests.
//
// Pool size defaults to min(runtime.NumCPU(), 8). Python startup is ~7-8 s per
// instance, so runners are initialized in parallel during Start.
type PythonDispatcher struct {
	cfg    Config
	log    *zap.Logger
	pool   chan *ResidentPythonRunner
	script string // cached content of eval.py
}

// NewPythonDispatcher creates a PythonDispatcher. Call Start before Send.
func NewPythonDispatcher(cfg Config, log *zap.Logger) *PythonDispatcher {
	return &PythonDispatcher{
		cfg: cfg,
		log: log.Named("dispatcher_python"),
	}
}

// Start reads the eval script, then initializes all pool runners in parallel.
func (d *PythonDispatcher) Start(ctx context.Context) error {
	if d.cfg.PythonScriptPath == "" {
		return fmt.Errorf("python-wasm: PythonScriptPath must be set (FUNCTION_WASM_PYTHON_SCRIPT)")
	}

	scriptBytes, err := os.ReadFile(d.cfg.PythonScriptPath)
	if err != nil {
		return fmt.Errorf("python-wasm: read script %q: %w", d.cfg.PythonScriptPath, err)
	}
	d.script = string(scriptBytes)

	// Determine pool size: cfg.MaxInstances if set, else NumCPU, capped at 8.
	poolSize := d.cfg.MaxInstances
	if poolSize <= 0 {
		poolSize = runtime.NumCPU()
	}
	if poolSize > 8 {
		poolSize = 8
	}

	d.log.Info("starting python-wasm dispatcher",
		zap.String("module", d.cfg.ModulePath),
		zap.String("script", d.cfg.PythonScriptPath),
		zap.Int("pool_size", poolSize),
	)

	d.pool = make(chan *ResidentPythonRunner, poolSize)

	// Initialize all runners in parallel (Python startup is slow).
	type result struct {
		runner *ResidentPythonRunner
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
			runner := NewResidentPythonRunner(d.cfg.ModulePath, d.cfg, d.log)
			if err := runner.Init(ctx); err != nil {
				results[i] = result{index: i, err: err}
				return
			}
			results[i] = result{index: i, runner: runner}
		}()
	}

	wg.Wait()

	// Collect runners; track which ones started successfully.
	var firstErr error
	var started []*ResidentPythonRunner

	for _, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("python-wasm: init runner %d: %w", r.index, r.err)
			}
		} else {
			started = append(started, r.runner)
		}
	}

	if firstErr != nil {
		// Shut down any runners that did start successfully.
		for _, runner := range started {
			_ = runner.Shutdown(ctx)
		}
		return firstErr
	}

	// All runners initialized; fill pool.
	for _, runner := range started {
		d.pool <- runner
	}

	d.log.Info("python-wasm dispatcher ready", zap.Int("pool_size", poolSize))
	return nil
}

// Send dispatches a request to a pooled ResidentPythonRunner.
//
// The "healthcheck" method is handled immediately without acquiring a runner.
// All other methods acquire a runner, call SendRequest with the script and
// JSON-marshalled params, then return the runner to the pool.
func (d *PythonDispatcher) Send(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	if method == "healthcheck" {
		return map[string]any{"status": "ok"}, nil
	}

	// Acquire a runner from the pool, respecting context cancellation.
	var runner *ResidentPythonRunner
	select {
	case runner = <-d.pool:
	case <-ctx.Done():
		return nil, fmt.Errorf("python-wasm: acquire runner: %w", ctx.Err())
	}

	defer func() {
		d.pool <- runner
	}()

	// Marshal params as the inputJSON argument.
	inputJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("python-wasm: marshal params: %w", err)
	}

	result, err := runner.SendRequest(ctx, d.script, string(inputJSON))
	if err != nil {
		return nil, fmt.Errorf("python-wasm: send request: %w", err)
	}

	return result, nil
}

// Shutdown drains the pool and shuts down each runner.
func (d *PythonDispatcher) Shutdown(ctx context.Context) error {
	d.log.Debug("shutting down python-wasm dispatcher")

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

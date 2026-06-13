package execution

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/dispatcher"
	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
	"github.com/lambda-feedback/shimmy/internal/execution/wasm"
)

type Dispatcher dispatcher.Dispatcher

type Config struct {
	// MaxWorkers is the maximum number of concurrent workers
	// when employing a pooled dispatcher.
	MaxWorkers int `conf:"max_workers"`

	// SupervisorConfig is the configuration to use for the supervisor
	Supervisor supervisor.Config `conf:",squash"`
}

type Params struct {
	// Context is the context to use for the dispatcher
	Context context.Context

	// Config is the config for the dispatcher and the underlying supervisors
	Config Config

	// Log is the logger to use for the dispatcher
	Log *zap.Logger
}

func NewDispatcher(params Params) (dispatcher.Dispatcher, error) {
	// wasmBaseConfig builds the Config fields shared by all WASM-backed
	// dispatchers (Wasm, PythonWasm, ReactorPython).
	wasmBaseConfig := func() wasm.Config {
		return wasm.Config{
			ModulePath:   params.Config.Supervisor.StartParams.Cmd,
			MaxInstances: params.Config.MaxWorkers,
			Timeout:      params.Config.Supervisor.SendParams.Timeout,
		}
	}

	switch params.Config.Supervisor.IO.Interface {
	case supervisor.WasmIO:
		cfg := wasmBaseConfig()
		d := wasm.NewDispatcher(cfg, params.Log)
		if err := d.Start(params.Context); err != nil {
			return nil, err
		}
		return d, nil

	case supervisor.PythonWasmIO:
		cfg := wasmBaseConfig()
		cfg.PythonScriptPath = os.Getenv("FUNCTION_WASM_PYTHON_SCRIPT")
		d := wasm.NewPythonDispatcher(cfg, params.Log)
		if err := d.Start(params.Context); err != nil {
			return nil, err
		}
		return d, nil

	case supervisor.ReactorPythonIO:
		if os.Getenv("FUNCTION_PYODIDE_ROOT") != "" || os.Getenv("FUNCTION_PYODIDE_EVAL_ENTRYPOINT") != "" {
			return nil, fmt.Errorf("reactor-python does not support package-style Lambda Feedback entrypoints yet; use FUNCTION_INTERFACE=pyodide with FUNCTION_PYODIDE_ROOT, FUNCTION_PYODIDE_EVAL_ENTRYPOINT, optional FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT, and FUNCTION_PYODIDE_ADAPTER")
		}

		cfg := wasmBaseConfig()
		cfg.PythonScriptPath = os.Getenv("FUNCTION_WASM_PYTHON_SCRIPT")
		d := wasm.NewReactorPythonDispatcher(cfg, params.Log)
		if err := d.Start(params.Context); err != nil {
			return nil, err
		}
		return d, nil

	case supervisor.PyodideIO:
		// Pyodide uses the rpc dispatcher with stdio transport.
		// Build a supervisor config that runs:
		//   node <runner_path> <script_path>
		// in legacy mode, or node <runner_path> in package mode.
		// Legacy mode still uses FUNCTION_PYODIDE_SCRIPT or argv[2]-style runners.
		// Package mode is selected when FUNCTION_PYODIDE_ROOT and
		// FUNCTION_PYODIDE_EVAL_ENTRYPOINT are both set.
		runnerPath := os.Getenv("FUNCTION_PYODIDE_RUNNER")
		if runnerPath == "" {
			runnerPath = "runner.js" // assume cwd contains runner.js
		}

		pyodideScriptPath := os.Getenv("FUNCTION_PYODIDE_SCRIPT")
		pyodideRoot := os.Getenv("FUNCTION_PYODIDE_ROOT")
		pyodideEvalEntrypoint := os.Getenv("FUNCTION_PYODIDE_EVAL_ENTRYPOINT")
		pyodidePackageMode := pyodideRoot != "" && pyodideEvalEntrypoint != ""

		pyodideSupervisorCfg := params.Config.Supervisor
		pyodideSupervisorCfg.IO.Interface = supervisor.RpcIO
		pyodideSupervisorCfg.IO.Rpc.Transport = supervisor.StdioTransport
		pyodideSupervisorCfg.StartParams.Cmd = "node"
		if pyodidePackageMode {
			pyodideSupervisorCfg.StartParams.Args = []string{runnerPath}
		} else {
			if pyodideScriptPath == "" {
				return nil, fmt.Errorf("pyodide: FUNCTION_PYODIDE_SCRIPT must be set (or provide FUNCTION_PYODIDE_ROOT + FUNCTION_PYODIDE_EVAL_ENTRYPOINT)")
			}
			pyodideSupervisorCfg.StartParams.Args = []string{runnerPath, pyodideScriptPath}
		}

		return dispatcher.NewDedicatedDispatcher(
			dispatcher.DedicatedDispatcherParams{
				Config: dispatcher.DedicatedDispatcherConfig{
					Supervisor: pyodideSupervisorCfg,
				},
				Context: params.Context,
				Log:     params.Log,
			},
		)

	case supervisor.RpcIO:
		return dispatcher.NewDedicatedDispatcher(
			dispatcher.DedicatedDispatcherParams{
				Config: dispatcher.DedicatedDispatcherConfig{
					Supervisor: params.Config.Supervisor,
				},
				Context: params.Context,
				Log:     params.Log,
			},
		)

	default:
		return dispatcher.NewPooledDispatcher(
			dispatcher.PooledDispatcherParams{
				Config: dispatcher.PooledDispatcherConfig{
					Supervisor: params.Config.Supervisor,
					MaxWorkers: params.Config.MaxWorkers,
				},
				Context: params.Context,
				Log:     params.Log,
			},
		)
	}
}

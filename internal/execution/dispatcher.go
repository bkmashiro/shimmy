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
	switch params.Config.Supervisor.IO.Interface {
	case supervisor.WasmIO:
		cfg := wasm.Config{
			ModulePath:   params.Config.Supervisor.StartParams.Cmd,
			MaxInstances: params.Config.MaxWorkers,
			Timeout:      params.Config.Supervisor.SendParams.Timeout,
		}

		d := wasm.NewDispatcher(cfg, params.Log)

		if err := d.Start(params.Context); err != nil {
			return nil, err
		}

		return d, nil

	case supervisor.PythonWasmIO:
		cfg := wasm.Config{
			ModulePath:       params.Config.Supervisor.StartParams.Cmd,
			MaxInstances:     params.Config.MaxWorkers,
			Timeout:          params.Config.Supervisor.SendParams.Timeout,
			PythonScriptPath: os.Getenv("FUNCTION_WASM_PYTHON_SCRIPT"),
		}

		d := wasm.NewPythonDispatcher(cfg, params.Log)
		if err := d.Start(params.Context); err != nil {
			return nil, err
		}
		return d, nil

	case supervisor.ReactorPythonIO:
		scriptPath := os.Getenv("FUNCTION_WASM_PYTHON_SCRIPT")

		cfg := wasm.Config{
			ModulePath:       params.Config.Supervisor.StartParams.Cmd,
			MaxInstances:     params.Config.MaxWorkers,
			Timeout:          params.Config.Supervisor.SendParams.Timeout,
			PythonScriptPath: scriptPath,
		}

		d := wasm.NewReactorPythonDispatcher(cfg, params.Log)
		if err := d.Start(params.Context); err != nil {
			return nil, err
		}
		return d, nil

	case supervisor.PyodideIO:
		// Pyodide uses the rpc dispatcher with stdio transport.
		// Build a supervisor config that runs:
		//   node <runner_path> <script_path>
		// where runner_path defaults to runner.js (in cwd)
		// and script_path comes from FUNCTION_PYODIDE_SCRIPT.
		runnerPath := os.Getenv("FUNCTION_PYODIDE_RUNNER")
		if runnerPath == "" {
			runnerPath = "runner.js" // assume cwd contains runner.js
		}
		scriptPath := os.Getenv("FUNCTION_PYODIDE_SCRIPT")
		if scriptPath == "" {
			return nil, fmt.Errorf("pyodide: FUNCTION_PYODIDE_SCRIPT must be set")
		}

		pyodideSupervisorCfg := params.Config.Supervisor
		pyodideSupervisorCfg.IO.Interface = supervisor.RpcIO
		pyodideSupervisorCfg.IO.Rpc.Transport = supervisor.StdioTransport
		pyodideSupervisorCfg.StartParams.Cmd = "node"
		pyodideSupervisorCfg.StartParams.Args = []string{runnerPath, scriptPath}

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

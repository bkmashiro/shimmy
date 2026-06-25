package execution

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

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
	case supervisor.RpcIO:
		if err := requireProcessWorkerCommand(params.Config.Supervisor); err != nil {
			return nil, err
		}
		return dispatcher.NewDedicatedDispatcher(
			dispatcher.DedicatedDispatcherParams{
				Config: dispatcher.DedicatedDispatcherConfig{
					Supervisor: params.Config.Supervisor,
				},
				Context: params.Context,
				Log:     params.Log,
			},
		)

	case supervisor.WasmIO:
		wasmProfile := strings.ToLower(strings.TrimSpace(os.Getenv("FUNCTION_WASM_PROFILE")))
		if wasmProfile == "" {
			wasmProfile = "generic"
		}
		if wasmProfile != "generic" {
			validProfiles := []string{"generic"}
			sort.Strings(validProfiles)
			return nil, fmt.Errorf("unsupported FUNCTION_WASM_PROFILE %q; supported values: %s", wasmProfile, strings.Join(validProfiles, ", "))
		}

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

	case supervisor.PyodideIO:
		return newPyodideDispatcher(params)

	default:
		if params.Config.Supervisor.IO.Interface == supervisor.FileIO {
			if err := requireProcessWorkerCommand(params.Config.Supervisor); err != nil {
				return nil, err
			}
		}
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

func requireProcessWorkerCommand(cfg supervisor.Config) error {
	if strings.TrimSpace(cfg.StartParams.Cmd) == "" {
		return fmt.Errorf("FUNCTION_COMMAND is required when FUNCTION_INTERFACE=%q", cfg.IO.Interface)
	}
	return nil
}

func newPyodideDispatcher(params Params) (dispatcher.Dispatcher, error) {
	runnerPath := strings.TrimSpace(os.Getenv("FUNCTION_PYODIDE_RUNNER"))
	if runnerPath == "" {
		runnerPath = "runner.js"
	}

	pyodideScriptPath := strings.TrimSpace(os.Getenv("FUNCTION_PYODIDE_SCRIPT"))
	pyodideRoot := strings.TrimSpace(os.Getenv("FUNCTION_PYODIDE_ROOT"))
	pyodideEvalEntrypoint := strings.TrimSpace(os.Getenv("FUNCTION_PYODIDE_EVAL_ENTRYPOINT"))
	pyodidePackageMode := pyodideRoot != "" && pyodideEvalEntrypoint != ""

	pyodideSupervisorCfg := params.Config.Supervisor
	pyodideSupervisorCfg.IO.Interface = supervisor.RpcIO
	pyodideSupervisorCfg.IO.Rpc.Transport = supervisor.StdioTransport
	pyodideSupervisorCfg.StartParams.Cmd = "node"
	if pyodidePackageMode {
		pyodideSupervisorCfg.StartParams.Args = []string{runnerPath}
	} else {
		if pyodideScriptPath == "" {
			return nil, fmt.Errorf("pyodide: FUNCTION_PYODIDE_SCRIPT must be set, or provide FUNCTION_PYODIDE_ROOT and FUNCTION_PYODIDE_EVAL_ENTRYPOINT")
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
}

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
	supervisorCfg, err := applyDBISecurityConfig(params.Config.Supervisor)
	if err != nil {
		return nil, err
	}

	switch supervisorCfg.IO.Interface {
	case supervisor.RpcIO:
		return dispatcher.NewDedicatedDispatcher(
			dispatcher.DedicatedDispatcherParams{
				Config: dispatcher.DedicatedDispatcherConfig{
					Supervisor: supervisorCfg,
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

		cfg := wasm.Config{
			ModulePath:   supervisorCfg.StartParams.Cmd,
			MaxInstances: params.Config.MaxWorkers,
			Timeout:      supervisorCfg.SendParams.Timeout,
		}
		switch wasmProfile {
		case "generic":
			d := wasm.NewDispatcher(cfg, params.Log)
			if err := d.Start(params.Context); err != nil {
				return nil, err
			}
			return d, nil
		case "python-reactor":
			cfg.PythonScriptPath = os.Getenv("FUNCTION_WASM_PYTHON_SCRIPT")
			d := wasm.NewAgentPythonDispatcher(cfg, params.Log)
			if err := d.Start(params.Context); err != nil {
				return nil, err
			}
			return d, nil
		default:
			validProfiles := []string{"generic", "python-reactor"}
			sort.Strings(validProfiles)
			return nil, fmt.Errorf("unsupported FUNCTION_WASM_PROFILE %q; supported values: %s", wasmProfile, strings.Join(validProfiles, ", "))
		}

	case supervisor.PyodideIO:
		runnerPath := os.Getenv("FUNCTION_PYODIDE_RUNNER")
		if runnerPath == "" {
			runnerPath = "runner.js"
		}
		scriptPath := os.Getenv("FUNCTION_PYODIDE_SCRIPT")
		rootPath := os.Getenv("FUNCTION_PYODIDE_ROOT")
		entrypoint := os.Getenv("FUNCTION_PYODIDE_EVAL_ENTRYPOINT")
		packageMode := rootPath != "" && entrypoint != ""

		cfg := supervisorCfg
		cfg.IO.Interface = supervisor.RpcIO
		cfg.IO.Rpc.Transport = supervisor.StdioTransport
		cfg.StartParams.Cmd = "node"
		if packageMode {
			cfg.StartParams.Args = []string{runnerPath}
		} else {
			if scriptPath == "" {
				return nil, fmt.Errorf("pyodide: FUNCTION_PYODIDE_SCRIPT must be set (or provide FUNCTION_PYODIDE_ROOT + FUNCTION_PYODIDE_EVAL_ENTRYPOINT)")
			}
			cfg.StartParams.Args = []string{runnerPath, scriptPath}
		}

		return dispatcher.NewDedicatedDispatcher(
			dispatcher.DedicatedDispatcherParams{
				Config:  dispatcher.DedicatedDispatcherConfig{Supervisor: cfg},
				Context: params.Context,
				Log:     params.Log,
			},
		)

	default:
		return dispatcher.NewPooledDispatcher(
			dispatcher.PooledDispatcherParams{
				Config: dispatcher.PooledDispatcherConfig{
					Supervisor: supervisorCfg,
					MaxWorkers: params.Config.MaxWorkers,
				},
				Context: params.Context,
				Log:     params.Log,
			},
		)
	}
}

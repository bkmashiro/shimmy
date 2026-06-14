package execution

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
		pythonScriptPath, err := reactorPythonScriptPath(params.Context)
		if err != nil {
			return nil, err
		}
		cfg.PythonScriptPath = pythonScriptPath
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

func reactorPythonScriptPath(ctx context.Context) (string, error) {
	root := os.Getenv("FUNCTION_LF_ROOT")
	evalEntrypoint := os.Getenv("FUNCTION_LF_EVAL_ENTRYPOINT")
	if root == "" && evalEntrypoint == "" {
		return os.Getenv("FUNCTION_WASM_PYTHON_SCRIPT"), nil
	}
	if root == "" || evalEntrypoint == "" {
		return "", fmt.Errorf("reactor-python Lambda Feedback package mode requires FUNCTION_LF_ROOT and FUNCTION_LF_EVAL_ENTRYPOINT")
	}
	return buildLambdaFeedbackBundle(ctx, lambdaFeedbackBundleConfig{
		Root:              root,
		EvalEntrypoint:    evalEntrypoint,
		PreviewEntrypoint: os.Getenv("FUNCTION_LF_PREVIEW_ENTRYPOINT"),
		AdapterRoot:       os.Getenv("FUNCTION_LF_ADAPTER_ROOT"),
		Bundler:           envDefault("FUNCTION_LF_BUNDLER", "tools/lf-bundle-python/lf_bundle_python.py"),
		Python:            envDefault("FUNCTION_LF_BUNDLE_PYTHON", "python3"),
		Out:               os.Getenv("FUNCTION_LF_BUNDLE_OUT"),
		IncludeRoots:      splitEnvList(os.Getenv("FUNCTION_LF_INCLUDE_ROOTS")),
		SysPath:           splitEnvList(os.Getenv("FUNCTION_LF_SYS_PATH")),
	})
}

type lambdaFeedbackBundleConfig struct {
	Root              string
	EvalEntrypoint    string
	PreviewEntrypoint string
	AdapterRoot       string
	Bundler           string
	Python            string
	Out               string
	IncludeRoots      []string
	SysPath           []string
}

func buildLambdaFeedbackBundle(ctx context.Context, cfg lambdaFeedbackBundleConfig) (string, error) {
	if cfg.AdapterRoot == "" {
		return "", fmt.Errorf("reactor-python Lambda Feedback package mode requires FUNCTION_LF_ADAPTER_ROOT")
	}
	out := cfg.Out
	if out == "" {
		file, err := os.CreateTemp("", "shimmy-lf-*.bundle.py")
		if err != nil {
			return "", fmt.Errorf("reactor-python: create Lambda Feedback bundle temp file: %w", err)
		}
		out = file.Name()
		if err := file.Close(); err != nil {
			return "", fmt.Errorf("reactor-python: close Lambda Feedback bundle temp file: %w", err)
		}
	}

	args := []string{
		cfg.Bundler,
		"--root", cfg.Root,
		"--adapter-root", cfg.AdapterRoot,
		"--eval-entrypoint", cfg.EvalEntrypoint,
		"--out", out,
	}
	if cfg.PreviewEntrypoint != "" {
		args = append(args, "--preview-entrypoint", cfg.PreviewEntrypoint)
	}
	for _, includeRoot := range cfg.IncludeRoots {
		args = append(args, "--include-root", includeRoot)
	}
	for _, sysPath := range cfg.SysPath {
		args = append(args, "--sys-path", sysPath)
	}

	cmd := exec.CommandContext(ctx, cfg.Python, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("reactor-python: bundle Lambda Feedback package: %w: %s", err, strings.TrimSpace(string(output)))
	}
	info, err := os.Stat(out)
	if err != nil {
		return "", fmt.Errorf("reactor-python: Lambda Feedback bundle was not written to %q: %w", out, err)
	}
	if info.Size() == 0 {
		return "", fmt.Errorf("reactor-python: Lambda Feedback bundle was empty: %q", out)
	}
	return out, nil
}

func envDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func splitEnvList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

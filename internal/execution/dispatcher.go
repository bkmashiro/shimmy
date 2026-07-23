package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	supervisorCfg, err := applyExecutionWrappers(params.Config.Supervisor)
	if err != nil {
		return nil, err
	}

	// wasmBaseConfig builds the Config fields shared by all WASM-backed
	// dispatchers (Wasm, PythonWasm, ReactorPython).
	wasmBaseConfig := func() wasm.Config {
		return wasm.Config{
			ModulePath:   supervisorCfg.StartParams.Cmd,
			MaxInstances: params.Config.MaxWorkers,
			Timeout:      supervisorCfg.SendParams.Timeout,
		}
	}

	newReactorPythonDispatcher := func() (dispatcher.Dispatcher, error) {
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
	}

	newGenericWasmDispatcher := func() (dispatcher.Dispatcher, error) {
		cfg := wasmBaseConfig()
		d := wasm.NewDispatcher(cfg, params.Log)
		if err := d.Start(params.Context); err != nil {
			return nil, err
		}
		return d, nil
	}

	validWasmProfiles := []string{"generic", "python-reactor", "reactor-python"}
	wasmProfile := strings.ToLower(strings.TrimSpace(os.Getenv("FUNCTION_WASM_PROFILE")))

	switch supervisorCfg.IO.Interface {
	case supervisor.WasmIO:
		if wasmProfile == "" {
			wasmProfile = "generic"
		}

		switch wasmProfile {
		case "generic":
			return newGenericWasmDispatcher()
		case "python-reactor", "reactor-python":
			return newReactorPythonDispatcher()
		default:
			sort.Strings(validWasmProfiles)
			return nil, fmt.Errorf("unsupported FUNCTION_WASM_PROFILE %q; supported values: %s", wasmProfile, strings.Join(validWasmProfiles, ", "))
		}

	case supervisor.PythonWasmIO:
		cfg := wasmBaseConfig()
		cfg.PythonScriptPath = os.Getenv("FUNCTION_WASM_PYTHON_SCRIPT")
		d := wasm.NewPythonDispatcher(cfg, params.Log)
		if err := d.Start(params.Context); err != nil {
			return nil, err
		}
		return d, nil

	case supervisor.ReactorPythonIO:
		return newReactorPythonDispatcher()

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

		pyodideSupervisorCfg := supervisorCfg
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
					Supervisor: supervisorCfg,
				},
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

func reactorPythonScriptPath(ctx context.Context) (string, error) {
	cfg, packageMode, err := reactorPythonLambdaFeedbackConfig()
	if err != nil {
		return "", err
	}
	if !packageMode {
		return os.Getenv("FUNCTION_WASM_PYTHON_SCRIPT"), nil
	}
	return buildLambdaFeedbackBundle(ctx, cfg)
}

type lambdaFeedbackConfigFile struct {
	Root              string   `json:"root"`
	Eval              string   `json:"eval"`
	EvalEntrypoint    string   `json:"eval_entrypoint"`
	Preview           string   `json:"preview"`
	PreviewEntrypoint string   `json:"preview_entrypoint"`
	AdapterRoot       string   `json:"adapter_root"`
	Bundler           string   `json:"bundler"`
	Python            string   `json:"python"`
	Out               string   `json:"out"`
	IncludeRoots      []string `json:"include_roots"`
	SysPath           []string `json:"sys_path"`
}

func reactorPythonLambdaFeedbackConfig() (lambdaFeedbackBundleConfig, bool, error) {
	fileCfg, err := readLambdaFeedbackConfigFile(os.Getenv("FUNCTION_LF_CONFIG"))
	if err != nil {
		return lambdaFeedbackBundleConfig{}, false, err
	}

	root := firstNonEmpty(os.Getenv("FUNCTION_LF_ROOT"), fileCfg.Root)
	evalEntrypoint := firstNonEmpty(os.Getenv("FUNCTION_LF_EVAL_ENTRYPOINT"), fileCfg.EvalEntrypoint, fileCfg.Eval)
	packageMode := root != "" || evalEntrypoint != "" || os.Getenv("FUNCTION_LF_CONFIG") != ""
	if !packageMode {
		return lambdaFeedbackBundleConfig{}, false, nil
	}
	if root == "" {
		return lambdaFeedbackBundleConfig{}, true, fmt.Errorf("reactor-python Lambda Feedback package mode requires FUNCTION_LF_ROOT")
	}
	if evalEntrypoint == "" {
		evalEntrypoint = "evaluation_function.evaluation:evaluation_function"
	}

	previewEntrypoint := firstNonEmpty(os.Getenv("FUNCTION_LF_PREVIEW_ENTRYPOINT"), fileCfg.PreviewEntrypoint, fileCfg.Preview)
	if previewEntrypoint == "" && fileExists(filepath.Join(root, "evaluation_function", "preview.py")) {
		previewEntrypoint = "evaluation_function.preview:preview_function"
	}

	includeRoots := fileCfg.IncludeRoots
	if envIncludeRoots := splitEnvList(os.Getenv("FUNCTION_LF_INCLUDE_ROOTS")); len(envIncludeRoots) > 0 {
		includeRoots = envIncludeRoots
	}
	sysPath := fileCfg.SysPath
	if envSysPath := splitEnvList(os.Getenv("FUNCTION_LF_SYS_PATH")); len(envSysPath) > 0 {
		sysPath = envSysPath
	}

	return lambdaFeedbackBundleConfig{
		Root:              root,
		EvalEntrypoint:    evalEntrypoint,
		PreviewEntrypoint: previewEntrypoint,
		AdapterRoot:       firstNonEmpty(os.Getenv("FUNCTION_LF_ADAPTER_ROOT"), fileCfg.AdapterRoot, "examples/lambda-feedback-adapter"),
		Bundler:           firstNonEmpty(os.Getenv("FUNCTION_LF_BUNDLER"), fileCfg.Bundler, "tools/lf-bundle-python/lf_bundle_python.py"),
		Python:            firstNonEmpty(os.Getenv("FUNCTION_LF_BUNDLE_PYTHON"), fileCfg.Python, "python3"),
		Out:               firstNonEmpty(os.Getenv("FUNCTION_LF_BUNDLE_OUT"), fileCfg.Out),
		IncludeRoots:      includeRoots,
		SysPath:           sysPath,
	}, true, nil
}

func readLambdaFeedbackConfigFile(path string) (lambdaFeedbackConfigFile, error) {
	if path == "" {
		return lambdaFeedbackConfigFile{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return lambdaFeedbackConfigFile{}, fmt.Errorf("reactor-python: read FUNCTION_LF_CONFIG %q: %w", path, err)
	}
	var cfg lambdaFeedbackConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return lambdaFeedbackConfigFile{}, fmt.Errorf("reactor-python: parse FUNCTION_LF_CONFIG %q: %w", path, err)
	}
	return cfg, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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

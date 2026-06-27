package execution

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
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

		cfg := wasm.Config{
			ModulePath:   params.Config.Supervisor.StartParams.Cmd,
			MaxInstances: params.Config.MaxWorkers,
			Timeout:      params.Config.Supervisor.SendParams.Timeout,
		}

		switch wasmProfile {
		case "generic":
			d := wasm.NewDispatcher(cfg, params.Log)
			if err := d.Start(params.Context); err != nil {
				return nil, err
			}
			return d, nil
		case "python-reactor":
			if err := validatePythonReactorConfig(&cfg); err != nil {
				return nil, err
			}
			return wasm.NewReactorPythonDispatcher(cfg, params.Log), nil
		default:
			validProfiles := []string{"generic", "python-reactor"}
			sort.Strings(validProfiles)
			return nil, fmt.Errorf("unsupported FUNCTION_WASM_PROFILE %q; supported values: %s", wasmProfile, strings.Join(validProfiles, ", "))
		}

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

func validatePythonReactorConfig(cfg *wasm.Config) error {
	if v := strings.TrimSpace(os.Getenv("FUNCTION_WASM_MODULE")); v != "" {
		cfg.ModulePath = v
	}
	cfg.PythonScriptPath = strings.TrimSpace(os.Getenv("FUNCTION_WASM_PYTHON_SCRIPT"))
	if strings.TrimSpace(cfg.ModulePath) == "" {
		return fmt.Errorf("reactor-python: FUNCTION_WASM_MODULE is required when FUNCTION_WASM_PROFILE=python-reactor")
	}
	if cfg.PythonScriptPath != "" {
		return nil
	}

	packageRoot := strings.TrimSpace(os.Getenv("FUNCTION_LF_ROOT"))
	if packageRoot == "" {
		return fmt.Errorf("reactor-python: FUNCTION_WASM_PYTHON_SCRIPT is required, or provide FUNCTION_LF_ROOT and FUNCTION_LF_EVAL_ENTRYPOINT for package mode")
	}
	evalEntrypoint := strings.TrimSpace(os.Getenv("FUNCTION_LF_EVAL_ENTRYPOINT"))
	if evalEntrypoint == "" {
		evalEntrypoint = "evaluation_function.evaluation:evaluation_function"
	}
	previewEntrypoint := strings.TrimSpace(os.Getenv("FUNCTION_LF_PREVIEW_ENTRYPOINT"))
	if previewEntrypoint == "" {
		previewEntrypoint = "evaluation_function.preview:preview_function"
	}

	bundle, err := writePythonReactorPackageBundle(packageRoot, evalEntrypoint, previewEntrypoint)
	if err != nil {
		return err
	}
	cfg.PythonScriptPath = bundle
	cfg.AllowedPaths = appendUniqueNonEmpty(cfg.AllowedPaths, packageRoot)
	if includeRoots := splitEnvList(os.Getenv("FUNCTION_LF_INCLUDE_ROOTS")); len(includeRoots) > 0 {
		cfg.AllowedPaths = appendUniqueNonEmpty(cfg.AllowedPaths, includeRoots...)
	}
	return nil
}

func writePythonReactorPackageBundle(root, evalEntrypoint, previewEntrypoint string) (string, error) {
	out := strings.TrimSpace(os.Getenv("FUNCTION_LF_BUNDLE_OUT"))
	var f *os.File
	var err error
	if out == "" {
		f, err = os.CreateTemp("", "shimmy-reactor-lf-*.py")
	} else {
		f, err = os.Create(out)
	}
	if err != nil {
		return "", fmt.Errorf("reactor-python: create package entrypoint bundle: %w", err)
	}
	defer f.Close()

	script := fmt.Sprintf(`
import dataclasses
import importlib
import inspect
import sys

_ROOT = %s
_EVAL_ENTRYPOINT = %s
_PREVIEW_ENTRYPOINT = %s

if _ROOT and _ROOT not in sys.path:
    sys.path.insert(0, _ROOT)


def _load_entrypoint(spec):
    if not spec or ':' not in spec:
        raise ValueError('Entrypoint must use module:function format')
    module_name, symbol_name = spec.rsplit(':', 1)
    module = importlib.import_module(module_name)
    return getattr(module, symbol_name)


def _normalize(value):
    if value is None or isinstance(value, (str, int, float, bool)):
        return value
    if isinstance(value, dict):
        return {k: _normalize(v) for k, v in value.items()}
    if isinstance(value, (list, tuple)):
        return [_normalize(v) for v in value]
    if hasattr(value, 'model_dump') and callable(getattr(value, 'model_dump')):
        return _normalize(value.model_dump())
    if hasattr(value, 'dict') and callable(getattr(value, 'dict')):
        return _normalize(value.dict())
    if dataclasses.is_dataclass(value) and not isinstance(value, type):
        return _normalize(dataclasses.asdict(value))
    fields = vars(value) if hasattr(value, '__dict__') else None
    if isinstance(fields, dict):
        return {k: _normalize(v) for k, v in fields.items() if not k.startswith('_')}
    return value


def _normalize_result(value):
    value = _normalize(value)
    if isinstance(value, dict):
        return value
    return {'value': value}


def _call(fn, method, response, answer=None, params=None):
    if params is None:
        params = {}
    if method == 'preview':
        try:
            sig = inspect.signature(fn)
            positional = [
                p for p in sig.parameters.values()
                if p.kind in (inspect.Parameter.POSITIONAL_ONLY, inspect.Parameter.POSITIONAL_OR_KEYWORD)
            ]
            has_varargs = any(p.kind == inspect.Parameter.VAR_POSITIONAL for p in sig.parameters.values())
            if not has_varargs and len(positional) == 2:
                return fn(response, params)
        except (TypeError, ValueError):
            pass
    return fn(response, answer, params)


def evaluation_function(response, answer, params=None):
    return _normalize_result(_call(_load_entrypoint(_EVAL_ENTRYPOINT), 'eval', response, answer, params))


def preview_function(response, answer=None, params=None):
    return _normalize_result(_call(_load_entrypoint(_PREVIEW_ENTRYPOINT), 'preview', response, answer, params))
`, strconv.Quote(root), strconv.Quote(evalEntrypoint), strconv.Quote(previewEntrypoint))
	if _, err := f.WriteString(script); err != nil {
		return "", fmt.Errorf("reactor-python: write package entrypoint bundle: %w", err)
	}
	return f.Name(), nil
}

func splitEnvList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func appendUniqueNonEmpty(base []string, values ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(values))
	out := make([]string, 0, len(base)+len(values))
	for _, v := range append(base, values...) {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
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

package execution_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution"
	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

// writeTempScript creates a temporary Python script file with the given
// content and returns its path. The file is cleaned up after the test.
func writeTempScript(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "eval_*.py")
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

func writeFakeBundler(t *testing.T, logPath string) string {
	t.Helper()
	bundler := filepath.Join(t.TempDir(), "fake_bundler.py")
	require.NoError(t, os.WriteFile(bundler, []byte(`
import pathlib
import sys
args = sys.argv[1:]
pathlib.Path("`+logPath+`").write_text("\n".join(args))
out = pathlib.Path(args[args.index("--out") + 1])
out.write_text("def evaluation_function(response, answer, params=None):\n    return {'is_correct': True}\n")
`), 0o755))
	return bundler
}

func TestNewDispatcher_Wasm_GenericProfile_DefaultsToGenericAndErrorsOnMissingModulePath(t *testing.T) {
	t.Setenv("FUNCTION_WASM_PROFILE", "")

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{
					Interface: supervisor.WasmIO,
				},
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "wasm: ModulePath must be set")
}

func TestNewDispatcher_Wasm_PythonReactorProfile_RoutesToReactorPython(t *testing.T) {
	t.Setenv("FUNCTION_WASM_PROFILE", "python-reactor")

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{
					Interface: supervisor.WasmIO,
				},
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "PythonScriptPath")
}

func TestNewDispatcher_Wasm_UnknownProfileErrorsWithValidValues(t *testing.T) {
	t.Setenv("FUNCTION_WASM_PROFILE", "ultra-bad-profile")

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{
					Interface: supervisor.WasmIO,
				},
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported FUNCTION_WASM_PROFILE "ultra-bad-profile"`)
	assert.Contains(t, err.Error(), "generic")
	assert.Contains(t, err.Error(), "shimmy-python")
	assert.Contains(t, err.Error(), "python-reactor")
	assert.Contains(t, err.Error(), "reactor-python")
}

func TestNewDispatcher_ReactorPythonInterface_IgnoresWasmProfile(t *testing.T) {
	t.Setenv("FUNCTION_WASM_PROFILE", "python-reactor")

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{
					Interface: supervisor.ReactorPythonIO,
				},
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "unsupported FUNCTION_WASM_PROFILE")
	assert.Contains(t, err.Error(), "PythonScriptPath")
}

func TestNewDispatcher_PyodideScriptModeRequiresScriptEnv(t *testing.T) {
	t.Setenv("FUNCTION_PYODIDE_SCRIPT", "")
	t.Setenv("FUNCTION_PYODIDE_ROOT", "")
	t.Setenv("FUNCTION_PYODIDE_EVAL_ENTRYPOINT", "")

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{Interface: supervisor.PyodideIO},
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FUNCTION_PYODIDE_SCRIPT must be set")
}

func TestNewDispatcher_PyodidePackageModeAcceptsEnvironmentConfig(t *testing.T) {
	t.Setenv("FUNCTION_PYODIDE_RUNNER", filepath.Join(t.TempDir(), "runner.js"))
	t.Setenv("FUNCTION_PYODIDE_ROOT", t.TempDir())
	t.Setenv("FUNCTION_PYODIDE_EVAL_ENTRYPOINT", "evaluation_function.evaluation:evaluation_function")
	t.Setenv("FUNCTION_PYODIDE_SCRIPT", "")

	d, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{Interface: supervisor.PyodideIO},
			},
		},
		Log: zap.NewNop(),
	})

	require.NoError(t, err)
	require.NotNil(t, d)
}

// TestNewDispatcher_ReactorPython_ExplicitInterface_EmptyModulePath verifies
// that an explicitly selected reactor-python interface takes the reactor path
// and fails with a ModulePath error (not a node/pyodide error).
func TestNewDispatcher_ReactorPython_ExplicitInterface_EmptyModulePath(t *testing.T) {
	script := writeTempScript(t, "import numpy as np\n\ndef evaluation_function(r, a, p):\n    return r == a\n")

	t.Setenv("FUNCTION_WASM_PYTHON_SCRIPT", script)
	// Ensure FUNCTION_PYODIDE_RUNNER is cleared — it won't be reached anyway.
	t.Setenv("FUNCTION_PYODIDE_RUNNER", "")

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{
					Interface: supervisor.ReactorPythonIO,
				},
				// Leave StartParams.Cmd empty → reactor-python runner will fail
				// with "ModulePath must be set".
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err, "reactor-python with empty ModulePath must fail")
	assert.Contains(t, err.Error(), "ModulePath",
		"error should be the reactor-python WASM config error, not a node/pyodide error")
}

func TestNewDispatcher_ReactorPython_RejectsPackageEntrypoints(t *testing.T) {
	t.Setenv("FUNCTION_PYODIDE_ROOT", t.TempDir())
	t.Setenv("FUNCTION_PYODIDE_EVAL_ENTRYPOINT", "evaluation_function.evaluation:evaluation_function")
	t.Setenv("FUNCTION_WASM_PYTHON_SCRIPT", writeTempScript(t, "def evaluation_function(r, a, p):\n    return {'is_correct': True}\n"))

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{
					Interface: supervisor.ReactorPythonIO,
				},
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reactor-python does not support package-style Lambda Feedback entrypoints yet")
	assert.Contains(t, err.Error(), "FUNCTION_INTERFACE=pyodide")
}

func TestNewDispatcher_ShimmyPythonRejectsRuntimeSysPath(t *testing.T) {
	t.Setenv("FUNCTION_LF_ROOT", t.TempDir())
	t.Setenv("FUNCTION_LF_SYS_PATH", "/opt/runtime-deps.zip")

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{Interface: supervisor.ReactorPythonIO},
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not expose Host filesystem paths")
	assert.Contains(t, err.Error(), "FUNCTION_LF_INCLUDE_ROOTS")
}

func TestNewDispatcher_ReactorPython_BundlesLambdaFeedbackPackageAtStartup(t *testing.T) {
	root := t.TempDir()
	adapterRoot := t.TempDir()
	out := filepath.Join(t.TempDir(), "generated.bundle.py")
	logPath := filepath.Join(t.TempDir(), "bundler.args")
	bundler := writeFakeBundler(t, logPath)

	t.Setenv("FUNCTION_LF_ROOT", root)
	t.Setenv("FUNCTION_LF_EVAL_ENTRYPOINT", "evaluation_function.evaluation:evaluation_function")
	t.Setenv("FUNCTION_LF_PREVIEW_ENTRYPOINT", "evaluation_function.preview:preview_function")
	t.Setenv("FUNCTION_LF_ADAPTER_ROOT", adapterRoot)
	t.Setenv("FUNCTION_LF_BUNDLER", bundler)
	t.Setenv("FUNCTION_LF_BUNDLE_OUT", out)
	t.Setenv("FUNCTION_LF_INCLUDE_ROOTS", "/opt/puredeps")

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{
					Interface: supervisor.ReactorPythonIO,
				},
				// Leave StartParams.Cmd empty; after bundling, reactor startup should
				// still fail on the missing/unsupported wasm module path.
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ModulePath")
	require.FileExists(t, out, "reactor package mode should generate the bundle before reactor startup")
	argsBytes, readErr := os.ReadFile(logPath)
	require.NoError(t, readErr)
	args := string(argsBytes)
	assert.Contains(t, args, "--root\n"+root)
	assert.Contains(t, args, "--adapter-root\n"+adapterRoot)
	assert.Contains(t, args, "--eval-entrypoint\nevaluation_function.evaluation:evaluation_function")
	assert.Contains(t, args, "--preview-entrypoint\nevaluation_function.preview:preview_function")
	assert.Contains(t, args, "--include-root\n/opt/puredeps")
	assert.Contains(t, args, "--out\n"+out)
}

func TestNewDispatcher_ReactorPython_DefaultsLambdaFeedbackEntrypoints(t *testing.T) {
	root := t.TempDir()
	adapterRoot := t.TempDir()
	out := filepath.Join(t.TempDir(), "generated.bundle.py")
	logPath := filepath.Join(t.TempDir(), "bundler.args")
	bundler := writeFakeBundler(t, logPath)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "evaluation_function"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "evaluation_function", "preview.py"), []byte("def preview_function():\n    return None\n"), 0o644))

	t.Setenv("FUNCTION_LF_ROOT", root)
	t.Setenv("FUNCTION_LF_ADAPTER_ROOT", adapterRoot)
	t.Setenv("FUNCTION_LF_BUNDLER", bundler)
	t.Setenv("FUNCTION_LF_BUNDLE_OUT", out)

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{
					Interface: supervisor.ReactorPythonIO,
				},
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ModulePath")
	require.FileExists(t, out, "reactor package mode should generate the bundle before reactor startup")
	argsBytes, readErr := os.ReadFile(logPath)
	require.NoError(t, readErr)
	args := string(argsBytes)
	assert.Contains(t, args, "--root\n"+root)
	assert.Contains(t, args, "--adapter-root\n"+adapterRoot)
	assert.Contains(t, args, "--eval-entrypoint\nevaluation_function.evaluation:evaluation_function")
	assert.Contains(t, args, "--preview-entrypoint\nevaluation_function.preview:preview_function")
}

func TestNewDispatcher_ReactorPython_LoadsLambdaFeedbackConfigFileWithEnvOverride(t *testing.T) {
	root := t.TempDir()
	adapterRoot := t.TempDir()
	out := filepath.Join(t.TempDir(), "generated.bundle.py")
	logPath := filepath.Join(t.TempDir(), "bundler.args")
	bundler := writeFakeBundler(t, logPath)
	configPath := filepath.Join(t.TempDir(), "shimmy-lf.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{
  "root": "`+root+`",
  "eval": "package.eval:evaluate",
  "preview": "package.preview:preview",
  "adapter_root": "`+adapterRoot+`",
  "bundler": "`+bundler+`",
  "out": "`+out+`",
  "include_roots": ["/opt/from-config"]
}`), 0o644))

	t.Setenv("FUNCTION_LF_CONFIG", configPath)
	t.Setenv("FUNCTION_LF_EVAL_ENTRYPOINT", "override.module:eval")

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{
					Interface: supervisor.ReactorPythonIO,
				},
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ModulePath")
	require.FileExists(t, out, "reactor package mode should generate the bundle before reactor startup")
	argsBytes, readErr := os.ReadFile(logPath)
	require.NoError(t, readErr)
	args := string(argsBytes)
	assert.Contains(t, args, "--root\n"+root)
	assert.Contains(t, args, "--adapter-root\n"+adapterRoot)
	assert.Contains(t, args, "--eval-entrypoint\noverride.module:eval")
	assert.Contains(t, args, "--preview-entrypoint\npackage.preview:preview")
	assert.Contains(t, args, "--include-root\n/opt/from-config")
}

// TestNewDispatcher_ReactorPython_EmptyScriptPath verifies that when
// FUNCTION_WASM_PYTHON_SCRIPT is not set, the dispatcher skips the import
// scan entirely and falls through to the reactor-python path, which then
// fails because PythonScriptPath is required.
func TestNewDispatcher_ReactorPython_EmptyScriptPath(t *testing.T) {
	t.Setenv("FUNCTION_WASM_PYTHON_SCRIPT", "")

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{
					Interface: supervisor.ReactorPythonIO,
				},
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err, "reactor-python with no script path must fail")
	assert.Contains(t, err.Error(), "PythonScriptPath",
		"empty script path should fall through to reactor-python, not Pyodide")
}

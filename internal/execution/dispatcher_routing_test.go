package execution_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution"
	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
	"github.com/lambda-feedback/shimmy/internal/execution/wasm"
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

// ---------------------------------------------------------------------------
// ScriptFileNeedsHeavyRuntime routing decisions
// ---------------------------------------------------------------------------

func TestScriptRouting_PureNumpy(t *testing.T) {
	script := writeTempScript(t, "import numpy as np\n\ndef evaluation_function(r, a, p):\n    return r == a\n")
	heavy, err := wasm.ScriptFileNeedsHeavyRuntime(script)
	require.NoError(t, err)
	assert.False(t, heavy, "pure numpy script should NOT route to Pyodide")
}

func TestScriptRouting_ScipyDetected(t *testing.T) {
	script := writeTempScript(t, "import numpy as np\nimport scipy.stats as stats\n\ndef evaluation_function(r, a, p):\n    return True\n")
	heavy, err := wasm.ScriptFileNeedsHeavyRuntime(script)
	require.NoError(t, err)
	assert.True(t, heavy, "script with scipy import should route to Pyodide")
}

func TestScriptRouting_CommentedScipyIgnored(t *testing.T) {
	script := writeTempScript(t, "# import scipy\nimport numpy as np\n\ndef evaluation_function(r, a, p):\n    return r == a\n")
	heavy, err := wasm.ScriptFileNeedsHeavyRuntime(script)
	require.NoError(t, err)
	assert.False(t, heavy, "commented-out scipy import should NOT trigger Pyodide routing")
}

func TestScriptRouting_PandasDetected(t *testing.T) {
	script := writeTempScript(t, "import pandas as pd\n\ndef evaluation_function(r, a, p):\n    return True\n")
	heavy, err := wasm.ScriptFileNeedsHeavyRuntime(script)
	require.NoError(t, err)
	assert.True(t, heavy, "script with pandas import should route to Pyodide")
}

func TestScriptRouting_FileNotFound(t *testing.T) {
	nonExistent := filepath.Join(t.TempDir(), "does_not_exist.py")
	heavy, err := wasm.ScriptFileNeedsHeavyRuntime(nonExistent)
	assert.Error(t, err, "non-existent file should return an error")
	assert.False(t, heavy, "file-not-found should return false so dispatcher falls back to reactor")
}

// TestNewDispatcher_ReactorPython_NoHeavyDeps_EmptyModulePath verifies that
// when there are no heavy deps the dispatcher takes the reactor-python code
// path and fails with a wasmPath error (not a node/pyodide error).
func TestNewDispatcher_ReactorPython_NoHeavyDeps_EmptyModulePath(t *testing.T) {
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
				// with "wasmPath must be set".
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err, "reactor-python with empty wasmPath must fail")
	if runtime.GOOS != "linux" {
		assert.Contains(t, err.Error(), "Linux",
			"non-Linux hosts should fail before reactor-python config validation")
		return
	}
	assert.Contains(t, err.Error(), "wasmPath",
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

func TestNewDispatcher_ReactorPython_BundlesLambdaFeedbackPackageAtStartup(t *testing.T) {
	root := t.TempDir()
	adapterRoot := t.TempDir()
	out := filepath.Join(t.TempDir(), "generated.bundle.py")
	logPath := filepath.Join(t.TempDir(), "bundler.args")
	bundler := filepath.Join(t.TempDir(), "fake_bundler.py")
	require.NoError(t, os.WriteFile(bundler, []byte(`
import pathlib
import sys
args = sys.argv[1:]
pathlib.Path("`+logPath+`").write_text("\n".join(args))
out = pathlib.Path(args[args.index("--out") + 1])
out.write_text("def evaluation_function(response, answer, params=None):\n    return {'is_correct': True}\n")
`), 0o755))

	t.Setenv("FUNCTION_LF_ROOT", root)
	t.Setenv("FUNCTION_LF_EVAL_ENTRYPOINT", "evaluation_function.evaluation:evaluation_function")
	t.Setenv("FUNCTION_LF_PREVIEW_ENTRYPOINT", "evaluation_function.preview:preview_function")
	t.Setenv("FUNCTION_LF_ADAPTER_ROOT", adapterRoot)
	t.Setenv("FUNCTION_LF_BUNDLER", bundler)
	t.Setenv("FUNCTION_LF_BUNDLE_OUT", out)
	t.Setenv("FUNCTION_LF_INCLUDE_ROOTS", strings.Join([]string{"/opt/puredeps", "/opt/polyfills"}, ","))
	t.Setenv("FUNCTION_LF_SYS_PATH", "/opt/puredeps.zip")

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
	if runtime.GOOS != "linux" {
		assert.Contains(t, err.Error(), "Linux")
	} else {
		assert.Contains(t, err.Error(), "wasmPath")
	}
	require.FileExists(t, out, "reactor package mode should generate the bundle before reactor startup")
	argsBytes, readErr := os.ReadFile(logPath)
	require.NoError(t, readErr)
	args := string(argsBytes)
	assert.Contains(t, args, "--root\n"+root)
	assert.Contains(t, args, "--adapter-root\n"+adapterRoot)
	assert.Contains(t, args, "--eval-entrypoint\nevaluation_function.evaluation:evaluation_function")
	assert.Contains(t, args, "--preview-entrypoint\nevaluation_function.preview:preview_function")
	assert.Contains(t, args, "--include-root\n/opt/puredeps")
	assert.Contains(t, args, "--include-root\n/opt/polyfills")
	assert.Contains(t, args, "--sys-path\n/opt/puredeps.zip")
	assert.Contains(t, args, "--out\n"+out)
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
	if runtime.GOOS != "linux" {
		assert.Contains(t, err.Error(), "Linux",
			"non-Linux hosts should fail before reactor-python script validation")
		return
	}
	assert.Contains(t, err.Error(), "PythonScriptPath",
		"empty script path should fall through to reactor-python, not Pyodide")
}

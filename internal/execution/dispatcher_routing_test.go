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

// ---------------------------------------------------------------------------
// NewDispatcher routing: ReactorPythonIO with heavy deps → Pyodide path
// ---------------------------------------------------------------------------

// TestNewDispatcher_ReactorPython_HeavyDepsRoutesToPyodide verifies that when
// FUNCTION_WASM_PYTHON_SCRIPT points to a script that imports scipy, NewDispatcher
// attempts the Pyodide (node) path rather than the reactor-python WASM path.
//
// Since node.js is unlikely to be present in CI, we assert that:
//  1. NewDispatcher returns an error (node process cannot start, or runner.js not found).
//  2. The error does NOT mention "wasmPath must be set" — that message comes from the
//     reactor-python WASM path and would indicate the routing decision was wrong.
func TestNewDispatcher_ReactorPython_HeavyDepsRoutesToPyodide(t *testing.T) {
	script := writeTempScript(t, "import scipy\nimport numpy as np\n\ndef evaluation_function(r, a, p):\n    return True\n")

	t.Setenv("FUNCTION_WASM_PYTHON_SCRIPT", script)
	// Use a clearly non-existent runner so the node invocation fails fast.
	t.Setenv("FUNCTION_PYODIDE_RUNNER", filepath.Join(t.TempDir(), "runner.js"))

	d, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			Supervisor: supervisor.Config{
				IO: supervisor.IOConfig{
					Interface: supervisor.ReactorPythonIO,
				},
				// Intentionally leave StartParams.Cmd empty — for the reactor
				// path this would trigger "wasmPath must be set". For the
				// Pyodide path the cmd is overridden to "node".
			},
		},
		Log: zap.NewNop(),
	})

	// NewDispatcher may succeed (returns a DedicatedDispatcher that hasn't
	// started yet) or fail immediately.  Either outcome is acceptable as long
	// as the reactor-python code path was NOT taken.
	if err != nil {
		// The error must NOT be the reactor-python "wasmPath must be set" sentinel —
		// that would mean the routing decision was wrong.
		assert.NotContains(t, err.Error(), "wasmPath",
			"error should come from the Pyodide/node path, not the reactor-python path")
	} else {
		// Dispatcher was created; clean it up.
		require.NotNil(t, d)
		_ = d.Shutdown(context.Background())
	}
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
	assert.Contains(t, err.Error(), "wasmPath",
		"error should be the reactor-python WASM config error, not a node/pyodide error")
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

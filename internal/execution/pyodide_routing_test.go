package execution_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution"
	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

func TestNewDispatcherPyodideScriptModeRequiresScript(t *testing.T) {
	t.Setenv("FUNCTION_PYODIDE_SCRIPT", "")
	t.Setenv("FUNCTION_PYODIDE_ROOT", "")
	t.Setenv("FUNCTION_PYODIDE_EVAL_ENTRYPOINT", "")

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			MaxWorkers: 1,
			Supervisor: supervisor.Config{IO: supervisor.IOConfig{Interface: supervisor.PyodideIO}},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FUNCTION_PYODIDE_SCRIPT must be set")
}

func TestNewDispatcherPyodidePackageModeAcceptsExplicitRoot(t *testing.T) {
	t.Setenv("FUNCTION_PYODIDE_RUNNER", filepath.Join(t.TempDir(), "runner.js"))
	t.Setenv("FUNCTION_PYODIDE_ROOT", t.TempDir())
	t.Setenv("FUNCTION_PYODIDE_EVAL_ENTRYPOINT", "evaluation_function.evaluation:evaluation_function")
	t.Setenv("FUNCTION_PYODIDE_SCRIPT", "")

	d, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			MaxWorkers: 1,
			Supervisor: supervisor.Config{IO: supervisor.IOConfig{Interface: supervisor.PyodideIO}},
		},
		Log: zap.NewNop(),
	})

	require.NoError(t, err)
	require.NotNil(t, d)
}

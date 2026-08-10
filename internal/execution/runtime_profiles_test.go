package execution_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution"
	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

func TestWASMProfileSelectionListsSupportedProfiles(t *testing.T) {
	t.Setenv("FUNCTION_WASM_PROFILE", "unknown")

	_, err := execution.NewDispatcher(execution.Params{
		Context: context.Background(),
		Config: execution.Config{
			MaxWorkers: 1,
			Supervisor: supervisor.Config{
				IO:          supervisor.IOConfig{Interface: supervisor.WasmIO},
				StartParams: supervisor.StartConfig{Cmd: "unused.wasm"},
			},
		},
		Log: zap.NewNop(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "generic, python-reactor")
}

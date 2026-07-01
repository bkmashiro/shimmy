package wasm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigApplyEnvSnapshotStrategy(t *testing.T) {
	t.Setenv("FUNCTION_WASM_SNAPSHOT_STRATEGY", "off")

	cfg := Config{}
	require.NoError(t, cfg.applyEnv())

	assert.Equal(t, SnapshotStrategyOff, cfg.SnapshotStrategy)
}

func TestConfigApplyDefaultsUsesFullSnapshotStrategy(t *testing.T) {
	cfg := Config{}
	cfg.applyDefaults()

	assert.Equal(t, SnapshotStrategyFull, cfg.SnapshotStrategy)
}

func TestConfigApplyEnvRejectsUnknownSnapshotStrategy(t *testing.T) {
	t.Setenv("FUNCTION_WASM_SNAPSHOT_STRATEGY", "magic")

	cfg := Config{}
	err := cfg.applyEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FUNCTION_WASM_SNAPSHOT_STRATEGY")
}

func TestNewSnapshotStrategyAllowsFullAndOff(t *testing.T) {
	full, err := NewSnapshotStrategy(SnapshotStrategyFull)
	require.NoError(t, err)
	assert.IsType(t, &FullMemcpyStrategy{}, full)

	off, err := NewSnapshotStrategy(SnapshotStrategyOff)
	require.NoError(t, err)
	assert.IsType(t, NoopSnapshotStrategy{}, off)
}

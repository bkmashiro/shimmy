package wasm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShimmyPythonLifecycleDefaultsToSnapshotMemcpy(t *testing.T) {
	cfg := Config{}
	cfg.applyShimmyPythonDefaults()

	assert.Equal(t, "snapshot", cfg.PythonLifecycle)
	assert.Equal(t, "memcpy", cfg.SnapshotMode)
	assert.Equal(t, 1, cfg.PythonPreparedCapacity)
	assert.Equal(t, uint64(8*1024*1024), cfg.PythonSnapshotHeadroomBytes)
	require.NoError(t, cfg.validateShimmyPythonLifecycle())
}

func TestShimmyPythonLifecycleReadsExplicitSingleUseCapacity(t *testing.T) {
	t.Setenv("FUNCTION_WASM_PYTHON_LIFECYCLE", "single-use")
	t.Setenv("FUNCTION_WASM_PYTHON_PREPARED_CAPACITY", "2")
	t.Setenv("FUNCTION_WASM_SHIMMY_PYTHON_EXPECTED_COMMIT", "0123456789abcdef0123456789abcdef01234567")
	cfg := Config{}
	cfg.applyEnv()
	cfg.applyShimmyPythonDefaults()

	assert.Equal(t, "single-use", cfg.PythonLifecycle)
	assert.Equal(t, 2, cfg.PythonPreparedCapacity)
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", cfg.ShimmyPythonExpectedCommit)
	require.NoError(t, cfg.validateShimmyPythonLifecycle())
}

func TestShimmyPythonLifecycleRejectsSnapshotStrategyOutsideSnapshotMode(t *testing.T) {
	cfg := Config{PythonLifecycle: "single-use", SnapshotMode: "cow"}
	cfg.applyShimmyPythonDefaults()

	err := cfg.validateShimmyPythonLifecycle()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only valid with lifecycle snapshot")
}

func TestShimmyPythonLifecycleRejectsUnknownAndOversizedCapacity(t *testing.T) {
	for _, cfg := range []Config{
		{PythonLifecycle: "reuse-maybe"},
		{PythonLifecycle: "single-use", PythonPreparedCapacity: 5},
		{PythonLifecycle: "snapshot", MaxInstances: 5},
	} {
		cfg.applyShimmyPythonDefaults()
		require.Error(t, cfg.validateShimmyPythonLifecycle())
	}
}

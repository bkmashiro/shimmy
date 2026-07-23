package wasm

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCowModeDoesNotChangeDefaultFullCopyStrategy(t *testing.T) {
	cfg := Config{}
	cfg.applyDefaults()

	require.Empty(t, cfg.SnapshotMode, "omitted mode remains the zero-value config representation")
	require.IsType(t, &FullMemcpyStrategy{}, selectSnapshotStrategy(cfg.SnapshotMode, nil, zap.NewNop()))
}

func TestCowSnapshotModeIsPoolSafeAndExplicit(t *testing.T) {
	cfg := Config{SnapshotMode: "cow"}
	require.NoError(t, cfg.validateSnapshotMode(8), "per-instance MAP_PRIVATE views are pool-safe")

	cfg = Config{UseUffd: true}
	cfg.applyDefaults()
	require.Equal(t, "uffd", cfg.SnapshotMode, "the deprecated UFFD flag must not select COW")
}

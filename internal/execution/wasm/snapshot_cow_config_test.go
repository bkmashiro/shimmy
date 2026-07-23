package wasm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCowSnapshotModeIsPoolSafeAndExplicit(t *testing.T) {
	cfg := Config{SnapshotMode: "cow"}
	require.NoError(t, cfg.validateSnapshotMode(8), "per-instance MAP_PRIVATE views are pool-safe")

	cfg = Config{UseUffd: true}
	cfg.applyDefaults()
	require.Equal(t, "uffd", cfg.SnapshotMode, "the deprecated UFFD flag must not select COW")
}

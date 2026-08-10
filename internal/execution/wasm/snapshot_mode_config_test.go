package wasm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateSnapshotModeAcceptsKnownValues(t *testing.T) {
	for _, mode := range []string{"", "memcpy", "soft-dirty", "mprotect", "uffd", "cow"} {
		t.Run(mode, func(t *testing.T) {
			cfg := Config{SnapshotMode: mode}
			require.NoError(t, cfg.validateSnapshotMode(1))
		})
	}
}

func TestValidateSnapshotModeRejectsUnknownAtEveryPoolSize(t *testing.T) {
	for _, poolSize := range []int{1, 8} {
		t.Run(string(rune('0'+poolSize)), func(t *testing.T) {
			cfg := Config{SnapshotMode: "typo"}
			err := cfg.validateSnapshotMode(poolSize)
			assert.ErrorContains(t, err, `snapshot mode "typo" is invalid`)
		})
	}
}

func TestValidateSnapshotModeRejectsProcessWideStrategiesForPools(t *testing.T) {
	for _, mode := range []string{"soft-dirty", "mprotect"} {
		t.Run(mode, func(t *testing.T) {
			cfg := Config{SnapshotMode: mode}
			assert.Error(t, cfg.validateSnapshotMode(2))
		})
	}
}

package wasm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestLogSnapshotSelectionRecordsRequestedSelectedAndFallbackReason(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	log := zap.New(core)

	logSnapshotSelection(log, "uffd", "memcpy", "userfaultfd unavailable")

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, zap.WarnLevel, entries[0].Level)
	fields := entries[0].ContextMap()
	assert.Equal(t, "uffd", fields["requested"])
	assert.Equal(t, "memcpy", fields["selected"])
	assert.Equal(t, "userfaultfd unavailable", fields["fallback_reason"])
}

func TestLogSnapshotSelectionNormalizesEmptyToMemcpy(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	log := zap.New(core)

	logSnapshotSelection(log, "", "memcpy", "")

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, zap.InfoLevel, entries[0].Level)
	fields := entries[0].ContextMap()
	assert.Equal(t, "memcpy", fields["requested"])
	assert.Equal(t, "memcpy", fields["selected"])
	assert.Equal(t, "", fields["fallback_reason"])
}

//go:build !linux

package wasm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestSelectSnapshotStrategyNonLinuxLogsExplicitFallback(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	strategy := selectSnapshotStrategy("uffd", nil, zap.New(core))
	defer strategy.Close() //nolint:errcheck

	_, isMemcpy := strategy.(*FullMemcpyStrategy)
	assert.True(t, isMemcpy)
	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, zap.WarnLevel, entries[0].Level)
	fields := entries[0].ContextMap()
	assert.Equal(t, "uffd", fields["requested"])
	assert.Equal(t, "memcpy", fields["selected"])
	assert.NotEmpty(t, fields["fallback_reason"])
}

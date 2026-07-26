//go:build linux && wasm_experimental

package wasm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSoftDirtyStrategyTakeRestoreRoundTrip(t *testing.T) {
	mem := newTestWazeroMemory(t, 2)
	pattern := make([]byte, mem.Size())
	for i := range pattern {
		pattern[i] = byte(i % 251)
	}
	require.True(t, mem.Write(0, pattern))

	strategy, err := NewSoftDirtyStrategy(mem)
	if err != nil {
		t.Skipf("soft-dirty unavailable: %v", err)
	}
	t.Cleanup(func() { _ = strategy.Close() })
	require.NoError(t, strategy.Take(mem))

	require.True(t, mem.Write(17, []byte("mutated-first-page")))
	require.True(t, mem.Write((64<<10)+23, []byte("mutated-second-page")))
	require.NoError(t, strategy.Restore(mem))

	restored, ok := mem.Read(0, mem.Size())
	require.True(t, ok)
	assert.Equal(t, pattern, []byte(restored))
}

func TestSoftDirtyStrategyCloseIsIdempotent(t *testing.T) {
	mem := newTestWazeroMemory(t, 1)
	strategy, err := NewSoftDirtyStrategy(mem)
	if err != nil {
		t.Skipf("soft-dirty unavailable: %v", err)
	}

	require.NoError(t, strategy.Take(mem))
	require.NoError(t, strategy.Close())
	require.NoError(t, strategy.Close())
	assert.Nil(t, strategy.snapshot)
}

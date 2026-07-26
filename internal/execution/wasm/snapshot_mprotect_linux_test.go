//go:build linux && cgo && wasm_experimental

package wasm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMprotectStrategyTakeRestoreRoundTrip(t *testing.T) {
	mem := newTestWazeroMemory(t, 2)
	pattern := make([]byte, mem.Size())
	for i := range pattern {
		pattern[i] = byte(i % 251)
	}
	require.True(t, mem.Write(0, pattern))

	strategy, err := NewMprotectStrategy(mem)
	if err != nil {
		t.Skipf("mprotect unavailable: %v", err)
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

func TestMprotectStrategyRejectsConcurrentInstance(t *testing.T) {
	firstMemory := newTestWazeroMemory(t, 1)
	first, err := NewMprotectStrategy(firstMemory)
	if err != nil {
		t.Skipf("mprotect unavailable: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })

	secondMemory := newTestWazeroMemory(t, 1)
	second, err := NewMprotectStrategy(secondMemory)
	assert.Nil(t, second)
	assert.ErrorContains(t, err, "only one active instance")
}

func TestMprotectStrategyCloseIsIdempotentAndAllowsReplacement(t *testing.T) {
	mem := newTestWazeroMemory(t, 1)
	strategy, err := NewMprotectStrategy(mem)
	if err != nil {
		t.Skipf("mprotect unavailable: %v", err)
	}

	require.NoError(t, strategy.Take(mem))
	require.NoError(t, strategy.Close())
	require.NoError(t, strategy.Close())
	require.Zero(t, mprotectActiveCount.Load())

	replacement, err := NewMprotectStrategy(mem)
	require.NoError(t, err)
	require.NoError(t, replacement.Close())
	assert.Zero(t, mprotectActiveCount.Load())
}

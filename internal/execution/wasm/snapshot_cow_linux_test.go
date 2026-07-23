//go:build linux

package wasm

import (
	"bytes"
	"errors"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func preparedCowPattern(size int) []byte {
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte((i*31 + 17) % 251)
	}
	return buf
}

func TestCowLinearMemoryCrossInstanceIsolationAndReset(t *testing.T) {
	const size = 4 * 4096
	baseline := preparedCowPattern(size)
	coordinator := newCowImageCoordinator()
	t.Cleanup(func() { require.NoError(t, coordinator.Close()) })

	first, err := newCowLinearMemory(size)
	require.NoError(t, err)
	t.Cleanup(first.Free)
	firstBytes := first.Reallocate(size)
	require.Len(t, firstBytes, size)
	copy(firstBytes, baseline)
	firstAddress := uintptr(unsafe.Pointer(unsafe.SliceData(firstBytes)))
	require.NoError(t, coordinator.PublishOrAttach(first))
	require.True(t, first.Attached())

	second, err := newCowLinearMemory(size)
	require.NoError(t, err)
	t.Cleanup(second.Free)
	secondBytes := second.Reallocate(size)
	require.Len(t, secondBytes, size)
	copy(secondBytes, baseline)
	secondAddress := uintptr(unsafe.Pointer(unsafe.SliceData(secondBytes)))
	require.NoError(t, coordinator.PublishOrAttach(second))
	require.True(t, second.Attached())
	require.NotEqual(t, firstAddress, secondAddress, "instances need distinct virtual address ranges")
	require.Equal(t, coordinator.ImageID(), first.ImageID())
	require.Equal(t, coordinator.ImageID(), second.ImageID())

	firstBytes = first.Bytes()
	secondBytes = second.Bytes()
	firstBytes[17] ^= 0xff
	firstBytes[4096+23] ^= 0xff
	require.Equal(t, baseline[17], secondBytes[17], "a private write must not leak into the other mapping")
	require.Equal(t, baseline[4096+23], secondBytes[4096+23])

	require.NoError(t, first.Reset())
	require.Equal(t, firstAddress, uintptr(unsafe.Pointer(unsafe.SliceData(first.Bytes()))), "reset must preserve the address wazero compiled against")
	require.Equal(t, baseline, first.Bytes(), "reset must restore the complete prepared image")
	require.Equal(t, baseline, second.Bytes(), "resetting one instance must not modify another")
}

func TestCowLinearMemoryRejectsMismatchedPreparedImage(t *testing.T) {
	const size = 2 * 4096
	baseline := preparedCowPattern(size)
	coordinator := newCowImageCoordinator()
	t.Cleanup(func() { require.NoError(t, coordinator.Close()) })

	canonical, err := newCowLinearMemory(size)
	require.NoError(t, err)
	t.Cleanup(canonical.Free)
	copy(canonical.Reallocate(size), baseline)
	require.NoError(t, coordinator.PublishOrAttach(canonical))

	mismatch, err := newCowLinearMemory(size)
	require.NoError(t, err)
	t.Cleanup(mismatch.Free)
	mismatchBytes := mismatch.Reallocate(size)
	copy(mismatchBytes, baseline)
	mismatchBytes[len(mismatchBytes)-1] ^= 0xff

	err = coordinator.PublishOrAttach(mismatch)
	require.ErrorIs(t, err, ErrCowBaselineMismatch)
	require.False(t, mismatch.Attached())
	require.NotEqual(t, baseline, mismatch.Bytes(), "a mismatch must never be overwritten with the canonical image")
}

func TestCowLinearMemoryRejectsGrowthAfterAttach(t *testing.T) {
	const initial = 2 * 4096
	const maximum = 4 * 4096
	coordinator := newCowImageCoordinator()
	t.Cleanup(func() { require.NoError(t, coordinator.Close()) })

	memory, err := newCowLinearMemory(maximum)
	require.NoError(t, err)
	t.Cleanup(memory.Free)
	initialBytes := memory.Reallocate(initial)
	copy(initialBytes, preparedCowPattern(initial))
	require.NoError(t, coordinator.PublishOrAttach(memory))
	address := uintptr(unsafe.Pointer(unsafe.SliceData(memory.Bytes())))

	require.Nil(t, memory.Reallocate(maximum), "memory.grow after baseline publication must fail closed")
	require.Len(t, memory.Bytes(), initial)
	require.Equal(t, address, uintptr(unsafe.Pointer(unsafe.SliceData(memory.Bytes()))))
	require.NoError(t, memory.Reset())
}

func TestCowLinearMemoryFreeIsIdempotent(t *testing.T) {
	memory, err := newCowLinearMemory(4096)
	require.NoError(t, err)
	require.Len(t, memory.Reallocate(4096), 4096)

	require.NotPanics(t, memory.Free)
	require.NotPanics(t, memory.Free)
	require.Nil(t, memory.Bytes())
	require.ErrorIs(t, memory.Reset(), ErrCowMemoryFreed)
}

func TestCowCoordinatorCloseRequiresNoLiveReset(t *testing.T) {
	coordinator := newCowImageCoordinator()
	memory, err := newCowLinearMemory(4096)
	require.NoError(t, err)
	t.Cleanup(memory.Free)
	copy(memory.Reallocate(4096), bytes.Repeat([]byte{0x5a}, 4096))
	require.NoError(t, coordinator.PublishOrAttach(memory))
	require.NoError(t, coordinator.Close())

	err = memory.Reset()
	require.True(t, errors.Is(err, ErrCowImageClosed) || errors.Is(err, ErrCowMemoryFreed))
}

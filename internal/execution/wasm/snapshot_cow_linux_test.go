//go:build linux

package wasm

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
)

//go:embed testdata/echo.wasm
var cowSupervisorEchoWasm []byte

func cleanupCowLinearMemory(t *testing.T, memory *cowLinearMemory) {
	t.Helper()
	t.Cleanup(func() {
		memory.Free()
		require.NoError(t, memory.Release())
	})
}

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
	cleanupCowLinearMemory(t, first)
	firstBytes := first.Reallocate(size)
	require.Len(t, firstBytes, size)
	copy(firstBytes, baseline)
	firstAddress := uintptr(unsafe.Pointer(unsafe.SliceData(firstBytes)))
	require.NoError(t, coordinator.PublishOrAttach(first))
	require.True(t, first.Attached())

	second, err := newCowLinearMemory(size)
	require.NoError(t, err)
	cleanupCowLinearMemory(t, second)
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
	cleanupCowLinearMemory(t, canonical)
	copy(canonical.Reallocate(size), baseline)
	require.NoError(t, coordinator.PublishOrAttach(canonical))

	mismatch, err := newCowLinearMemory(size)
	require.NoError(t, err)
	cleanupCowLinearMemory(t, mismatch)
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
	cleanupCowLinearMemory(t, memory)
	initialBytes := memory.Reallocate(initial)
	copy(initialBytes, preparedCowPattern(initial))
	require.NoError(t, coordinator.PublishOrAttach(memory))
	address := uintptr(unsafe.Pointer(unsafe.SliceData(memory.Bytes())))

	require.Nil(t, memory.Reallocate(maximum), "memory.grow after baseline publication must fail closed")
	require.Len(t, memory.Bytes(), initial)
	require.Equal(t, address, uintptr(unsafe.Pointer(unsafe.SliceData(memory.Bytes()))))
	require.NoError(t, memory.Reset())
}

func TestCowLinearMemoryFreeDefersUnmapUntilRelease(t *testing.T) {
	memory, err := newCowLinearMemory(4096)
	require.NoError(t, err)
	view := memory.Reallocate(4096)
	require.Len(t, view, 4096)
	view[0] = 0x5a

	require.NotPanics(t, memory.Free)
	require.NotPanics(t, memory.Free)
	require.Nil(t, memory.Bytes())
	require.ErrorIs(t, memory.Reset(), ErrCowMemoryFreed)
	// wazero can still touch MemoryInstance.Buffer while its native call engine
	// unwinds after cancellation. Free invalidates logical use, but Release owns
	// the later point where revoking the virtual address range is safe.
	require.Equal(t, byte(0x5a), view[0])
	require.NotEmpty(t, memory.region)

	require.NoError(t, memory.Release())
	require.NoError(t, memory.Release())
	require.Nil(t, memory.region)
}

func TestCowMemoryAllocatorCloseReleasesEveryBacking(t *testing.T) {
	allocator := newCowMemoryAllocator()
	first := allocator.Allocate(4096, 4096).(*cowLinearMemory)
	second := allocator.Allocate(4096, 4096).(*cowLinearMemory)
	require.Len(t, first.Reallocate(4096), 4096)
	require.Len(t, second.Reallocate(4096), 4096)
	first.Free()
	second.Free()

	require.NoError(t, allocator.Close())
	require.NoError(t, allocator.Close())
	require.Nil(t, first.region)
	require.Nil(t, second.region)
}

func TestCowWasmSupervisorShutdownReleasesBacking(t *testing.T) {
	ctx := context.Background()
	rt, compiled := compileEchoModule(t, ctx, cowSupervisorEchoWasm)
	t.Cleanup(func() { _ = rt.Close(ctx) })
	coordinator := newCowImageCoordinator()
	t.Cleanup(func() { require.NoError(t, coordinator.Close()) })

	supervisor := newWasmSupervisor(
		rt,
		compiled,
		wazero.NewModuleConfig().WithName(""),
		5*time.Second,
		"cow",
		newTestLogger(t),
		coordinator,
	)
	require.NoError(t, supervisor.Start(ctx))
	require.NotNil(t, supervisor.cowSupport)
	backing, err := supervisor.cowSupport.allocator.backingFor(supervisor.mod.Memory())
	require.NoError(t, err)
	require.NotEmpty(t, backing.region)

	require.NoError(t, supervisor.Shutdown(ctx))
	require.NoError(t, supervisor.Shutdown(ctx))
	require.Nil(t, backing.region)
	require.Nil(t, supervisor.cowSupport)
}

func TestCowCoordinatorCloseRequiresNoLiveReset(t *testing.T) {
	coordinator := newCowImageCoordinator()
	memory, err := newCowLinearMemory(4096)
	require.NoError(t, err)
	cleanupCowLinearMemory(t, memory)
	copy(memory.Reallocate(4096), bytes.Repeat([]byte{0x5a}, 4096))
	require.NoError(t, coordinator.PublishOrAttach(memory))
	require.NoError(t, coordinator.Close())

	err = memory.Reset()
	require.True(t, errors.Is(err, ErrCowImageClosed) || errors.Is(err, ErrCowMemoryFreed))
}

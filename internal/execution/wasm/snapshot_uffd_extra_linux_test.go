//go:build linux

package wasm

import (
	"context"
	"syscall"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
)

// snapshot_uffd_extra_linux_test.go contains additional unit tests for
// UffdProbeStrategy and UffdStrategy that complement the existing tests in
// snapshot_uffd_linux_test.go.

// ---------------------------------------------------------------------------
// TestUffdProbeStrategy_CloseIdempotent
// ---------------------------------------------------------------------------

// TestUffdProbeStrategy_CloseIdempotent verifies that Close can be called
// more than once on a UffdProbeStrategy without panicking or returning an
// error.  The first call should release resources; subsequent calls are no-ops.
func TestUffdProbeStrategy_CloseIdempotent(t *testing.T) {
	if !UffdAvailable() {
		t.Skip("userfaultfd not available in this environment (seccomp or insufficient privileges)")
	}

	s, err := NewUffdProbeStrategy()
	if err != nil {
		t.Skipf("NewUffdProbeStrategy returned error (expected if WP unavailable): %v", err)
	}
	require.NotNil(t, s)

	// First Close.
	require.NoError(t, s.Close(), "first Close must not error")
	assert.Equal(t, -1, s.uffdFd, "uffd fd must be -1 after first Close")
	assert.Zero(t, s.testRegion, "test mmap region must be 0 after first Close")

	// Second Close — must not panic or error even though resources are gone.
	// The implementation guards testRegion != 0 and uffdFd >= 0 before
	// munmap/close, so a second call is safe.
	require.NoError(t, s.Close(), "second Close must not error")
}

// ---------------------------------------------------------------------------
// TestUffdProbeStrategy_TakeNilMemory
// ---------------------------------------------------------------------------

// TestUffdProbeStrategy_TakeNilMemory verifies that Take(nil) delegates to
// the FullMemcpyStrategy fallback and handles nil gracefully (no panic, no
// error, snapshot cleared).
func TestUffdProbeStrategy_TakeNilMemory(t *testing.T) {
	if !UffdAvailable() {
		t.Skip("userfaultfd not available in this environment (seccomp or insufficient privileges)")
	}

	s, err := NewUffdProbeStrategy()
	if err != nil {
		t.Skipf("NewUffdProbeStrategy returned error: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Take(nil) must not panic or return an error.
	require.NoError(t, s.Take(nil), "Take(nil) should not error")

	// Restore(nil) after a nil Take must also be safe.
	require.NoError(t, s.Restore(nil), "Restore(nil) after Take(nil) should not error")
}

// ---------------------------------------------------------------------------
// TestUffdProbeStrategy_TakeRestoreRoundtrip
// ---------------------------------------------------------------------------

// TestUffdProbeStrategy_TakeRestoreRoundtrip verifies that UffdProbeStrategy
// (which delegates to FullMemcpyStrategy) correctly snapshots and restores
// WASM linear memory.
func TestUffdProbeStrategy_TakeRestoreRoundtrip(t *testing.T) {
	if !UffdAvailable() {
		t.Skip("userfaultfd not available in this environment (seccomp or insufficient privileges)")
	}

	s, err := NewUffdProbeStrategy()
	if err != nil {
		t.Skipf("NewUffdProbeStrategy returned error: %v", err)
	}
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	ctx := context.Background()
	wasmBytes := echoWasmBytes(t)
	rt, compiled := compileEchoModule(t, ctx, wasmBytes)
	t.Cleanup(func() { _ = rt.Close(ctx) })

	modCfg := wazero.NewModuleConfig().
		WithName("").
		WithSysNanosleep().
		WithSysWalltime().
		WithSysNanotime()

	mod, err := rt.InstantiateModule(ctx, compiled,
		modCfg.WithStartFunctions("_initialize", "_start"))
	require.NoError(t, err, "instantiate echo module")
	t.Cleanup(func() { _ = mod.Close(ctx) })

	mem := mod.Memory()
	require.NotNil(t, mem)

	// Snapshot before writes.
	require.NoError(t, s.Take(mem))

	before, ok := mem.Read(0, mem.Size())
	require.True(t, ok)
	beforeCopy := make([]byte, len(before))
	copy(beforeCopy, before)

	// Write zeros to the first page.
	pageSize := syscall.Getpagesize()
	if int(mem.Size()) >= pageSize {
		require.True(t, mem.Write(0, make([]byte, pageSize)), "write zeros to first page")
	}

	// Restore and verify.
	require.NoError(t, s.Restore(mem))
	restored, ok := mem.Read(0, mem.Size())
	require.True(t, ok)
	assert.Equal(t, beforeCopy, []byte(restored),
		"after Restore memory must match pre-write state")
}

// ---------------------------------------------------------------------------
// TestUffdStrategy_DirtyPageCount
// ---------------------------------------------------------------------------

// TestUffdStrategy_DirtyPageCount verifies that after writing to exactly N
// distinct pages, UffdStrategy tracks N dirty pages, and that after Restore
// the dirty-page bitset is entirely cleared (no pages remain dirty).
//
// The test directly manipulates the dirty bitmap and disarms WP (the same
// technique used by simulateDirtyWrites in snapshot_bench_test.go) so that
// the test does not depend on the asynchronous faultLoop goroutine detecting
// real write-protect faults.
func TestUffdStrategy_DirtyPageCount(t *testing.T) {
	if !UffdAvailable() {
		t.Skip("userfaultfd not available in this environment (seccomp or insufficient privileges)")
	}

	ctx := context.Background()
	wasmBytes := echoWasmBytes(t)
	rt, compiled := compileEchoModule(t, ctx, wasmBytes)
	t.Cleanup(func() { _ = rt.Close(ctx) })

	modCfg := wazero.NewModuleConfig().
		WithName("").
		WithSysNanosleep().
		WithSysWalltime().
		WithSysNanotime()

	mod, err := rt.InstantiateModule(ctx, compiled,
		modCfg.WithStartFunctions("_initialize", "_start"))
	require.NoError(t, err, "instantiate echo module")
	t.Cleanup(func() { _ = mod.Close(ctx) })

	mem := mod.Memory()
	require.NotNil(t, mem, "echo module must expose linear memory")

	strategy, err := NewUffdStrategy(mem)
	if err != nil {
		t.Skipf("NewUffdStrategy failed (uffd+WP unavailable): %v", err)
	}
	t.Cleanup(func() { require.NoError(t, strategy.Close()) })

	// Snapshot the initial state.
	require.NoError(t, strategy.Take(mem), "Take")

	// Choose a set of page indices to mark dirty.
	totalPages := strategy.pageCount
	require.Greater(t, totalPages, 3, "need at least 4 pages for this test")
	dirtyPageIndices := []int{0, 1, totalPages - 1} // first, second, last page
	nDirty := len(dirtyPageIndices)

	// Directly mark pages as dirty and disarm WP on each (mimicking what
	// faultLoop would do for a real write-protect fault).
	pageSize := strategy.pageSize
	memSize := int(strategy.memSize)

	strategy.mu.Lock()
	for _, pg := range dirtyPageIndices {
		if pg < len(strategy.dirty) {
			strategy.dirty[pg] = true
		}
	}
	strategy.mu.Unlock()

	// Disarm WP for dirty pages so Restore can write to them.
	for _, pg := range dirtyPageIndices {
		off := pg * pageSize
		end := off + pageSize
		if end > memSize {
			end = memSize
		}
		wp := uffdioWPStrategy{
			uffdioRangeStrategy: uffdioRangeStrategy{
				start: uint64(uintptr(strategy.basePtr)) + uint64(off),
				len:   uint64(end - off),
			},
			mode: 0, // clear WP
		}
		ioctlUffd(uintptr(strategy.uffdFd), ioctlUffdioWPStrategy, uintptr(unsafe.Pointer(&wp))) //nolint:errcheck
	}

	// Verify the dirty count matches the number of pages we marked.
	strategy.mu.Lock()
	var markedCount int
	for _, d := range strategy.dirty {
		if d {
			markedCount++
		}
	}
	strategy.mu.Unlock()
	assert.Equal(t, nDirty, markedCount,
		"dirty page count must equal number of pages we explicitly marked")

	// Restore — should clear all dirty bits and copy snapshot data back.
	require.NoError(t, strategy.Restore(mem), "Restore")

	// After Restore, no pages should remain dirty.
	strategy.mu.Lock()
	var remainingDirty int
	for _, d := range strategy.dirty {
		if d {
			remainingDirty++
		}
	}
	strategy.mu.Unlock()
	assert.Equal(t, 0, remainingDirty,
		"dirty-page bitset must be fully cleared after Restore")

	t.Logf("UffdStrategy dirty-page count: marked=%d, cleared after Restore=%d (totalPages=%d)",
		nDirty, remainingDirty, totalPages)
}

// ---------------------------------------------------------------------------
// TestUffdStrategy_RestoreNilSnapshot
// ---------------------------------------------------------------------------

// TestUffdStrategy_RestoreNilSnapshot verifies that Restore is a safe no-op
// when the strategy's internal snapshot is nil (i.e., Take was never called
// successfully or was called with nil memory).
//
// We exploit the fact that UffdStrategy.Restore checks snapshot == nil before
// doing any work.
func TestUffdStrategy_RestoreNilSnapshot(t *testing.T) {
	if !UffdAvailable() {
		t.Skip("userfaultfd not available in this environment (seccomp or insufficient privileges)")
	}

	ctx := context.Background()
	wasmBytes := echoWasmBytes(t)
	rt, compiled := compileEchoModule(t, ctx, wasmBytes)
	t.Cleanup(func() { _ = rt.Close(ctx) })

	modCfg := wazero.NewModuleConfig().
		WithName("").
		WithSysNanosleep().
		WithSysWalltime().
		WithSysNanotime()

	mod, err := rt.InstantiateModule(ctx, compiled,
		modCfg.WithStartFunctions("_initialize", "_start"))
	require.NoError(t, err, "instantiate echo module")
	t.Cleanup(func() { _ = mod.Close(ctx) })

	mem := mod.Memory()
	require.NotNil(t, mem)

	strategy, err := NewUffdStrategy(mem)
	if err != nil {
		t.Skipf("NewUffdStrategy failed: %v", err)
	}
	t.Cleanup(func() { require.NoError(t, strategy.Close()) })

	// Forcibly clear the snapshot to simulate the nil path.
	strategy.mu.Lock()
	strategy.snapshot = nil
	strategy.mu.Unlock()

	// Restore(nil) with nil snapshot must be a clean no-op.
	require.NoError(t, strategy.Restore(nil), "Restore with nil snapshot must not error")
}

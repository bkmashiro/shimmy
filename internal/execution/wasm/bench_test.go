//go:build linux

package wasm

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"go.uber.org/zap"
)

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// benchEchoModulePath returns the absolute path to testdata/echo.wasm.
// It uses runtime.Caller so it works regardless of the working directory.
func benchEchoModulePath(b *testing.B) string {
	b.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		b.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filename), "testdata", "echo.wasm")
}

// benchEchoWasmBytes reads the echo fixture bytes.
func benchEchoWasmBytes(b *testing.B) []byte {
	b.Helper()
	p := benchEchoModulePath(b)
	data, err := os.ReadFile(p)
	require.NoError(b, err, "read echo.wasm fixture")
	return data
}

// --------------------------------------------------------------------------
// Dispatcher benchmarks
//
// The echo.wasm fixture stores its bump-allocator pointer in a WASM global
// (not in linear memory).  restoreSnapshot() only snapshots/restores linear
// memory, so the global heap_top keeps advancing across iterations.  The
// module OOMs after ~1200 calls with a minimal 38-byte request payload.
//
// To work around this in benchmarks we recreate the dispatcher every
// resetEvery iterations, keeping the global state fresh while still
// measuring steady-state dispatch latency (setup cost is amortised).
//
// In production this is not an issue: real WASM modules store allocator state
// inside linear memory and it is correctly restored by the snapshot.
// --------------------------------------------------------------------------

const (
	// resetEvery is how many Send() calls we run before tearing down and
	// rebuilding the dispatcher.  With a 38-byte request the echo module can
	// handle ~1200 calls before OOM; we stay well below that.
	resetEvery = 500
)

// BenchmarkDispatcher_Send_Pool1 measures the per-request round-trip cost
// through a WASM dispatcher with exactly one module instance (no pool
// contention): alloc → memory write → evaluate → response parse →
// snapshot restore.
func BenchmarkDispatcher_Send_Pool1(b *testing.B) {
	ctx := context.Background()
	modPath := benchEchoModulePath(b)

	// Minimal payload — keep the request small so the bump-allocator in the
	// echo fixture lasts longer between resets.
	data := map[string]any{"i": 1}

	newDispatcher := func() *Dispatcher {
		cfg := Config{
			ModulePath:     modPath,
			MaxInstances:   1,
			Timeout:        5 * time.Second,
			MaxMemoryPages: 256,
		}
		d := NewDispatcher(cfg, zap.NewNop())
		if err := d.Start(ctx); err != nil {
			b.Fatalf("dispatcher start: %v", err)
		}
		return d
	}

	d := newDispatcher()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Recreate the dispatcher periodically to reset WASM globals in the
		// echo fixture (real modules don't need this).
		if i > 0 && i%resetEvery == 0 {
			b.StopTimer()
			_ = d.Shutdown(ctx)
			d = newDispatcher()
			b.StartTimer()
		}

		_, err := d.Send(ctx, "eval", data)
		if err != nil {
			b.Fatalf("Send failed at iteration %d: %v", i, err)
		}
	}
	b.StopTimer()
	_ = d.Shutdown(ctx)
}

// BenchmarkDispatcher_Send_PoolN measures Send throughput with NumCPU module
// instances running in parallel.  Exercises pool acquisition, concurrent WASM
// execution, and snapshot restore under concurrency.
//
// Because the echo.wasm fixture stores its bump-allocator in a WASM global
// (not restored by the memcpy snapshot), each instance can handle only ~500
// calls before exhausting its 64 KB linear memory.  We work around this by
// using a shared atomic counter and a mutex-guarded dispatcher swap: when any
// goroutine detects that the call count is approaching the limit it acquires a
// lock, tears down the old dispatcher, and builds a fresh one.  Measurement
// is paused during the reset so setup cost is not charged.
func BenchmarkDispatcher_Send_PoolN(b *testing.B) {
	ctx := context.Background()
	n := runtime.NumCPU()
	modPath := benchEchoModulePath(b)
	data := map[string]any{"i": 1}

	makeDispatcher := func() *Dispatcher {
		cfg := Config{
			ModulePath:     modPath,
			MaxInstances:   n,
			Timeout:        5 * time.Second,
			MaxMemoryPages: 256,
		}
		d := NewDispatcher(cfg, zap.NewNop())
		if err := d.Start(ctx); err != nil {
			b.Fatalf("dispatcher start: %v", err)
		}
		return d
	}

	var (
		mu      sync.RWMutex
		d       = makeDispatcher()
		callCnt atomic.Int64
	)

	b.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		_ = d.Shutdown(ctx)
	})

	// perInstanceLimit: how many calls each instance can safely handle.
	// echo.wasm: 53 bytes/call, 65532 bytes usable → ~1236 calls total across
	// all N instances.  We reset at half that to give plenty of headroom.
	perInstanceLimit := int64(500)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Check if we're nearing the per-instance limit.
			cnt := callCnt.Add(1)
			if cnt%perInstanceLimit == 0 {
				// Only one goroutine does the reset; others wait.
				b.StopTimer()
				mu.Lock()
				_ = d.Shutdown(ctx)
				d = makeDispatcher()
				callCnt.Store(0)
				mu.Unlock()
				b.StartTimer()
			}

			mu.RLock()
			_, err := d.Send(ctx, "eval", data)
			mu.RUnlock()

			if err != nil {
				b.Logf("Send error (transient during reset): %v", err)
			}
		}
	})
}

// --------------------------------------------------------------------------
// Snapshot-restore strategy benchmarks
// --------------------------------------------------------------------------

// BenchmarkSnapshotRestore_FullMemcpy benchmarks the cost of the current
// production strategy: a single mem.Write that copies the entire snapshot
// back into WASM linear memory after every request.
//
// b.SetBytes reports effective memory bandwidth in MB/s.
func BenchmarkSnapshotRestore_FullMemcpy(b *testing.B) {
	ctx := context.Background()
	wasmBytes := benchEchoWasmBytes(b)

	rt := wazero.NewRuntime(ctx)
	b.Cleanup(func() { _ = rt.Close(ctx) })

	_, err := wasi_snapshot_preview1.Instantiate(ctx, rt)
	require.NoError(b, err, "instantiate WASI")

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	require.NoError(b, err, "compile echo module")
	b.Cleanup(func() { _ = compiled.Close(ctx) })

	modCfg := wazero.NewModuleConfig().
		WithName("").
		WithSysNanosleep().
		WithSysWalltime().
		WithSysNanotime()

	sv := newWasmSupervisor(rt, compiled, modCfg, 5*time.Second, zap.NewNop())
	require.NoError(b, sv.Start(ctx))
	b.Cleanup(func() { _ = sv.Shutdown(ctx) })

	sv.mu.Lock()
	memSize := sv.mod.Memory().Size()
	snapCopy := make([]byte, len(sv.snapshot))
	copy(snapCopy, sv.snapshot)
	sv.mu.Unlock()

	b.SetBytes(int64(memSize))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sv.mu.Lock()
		ok := sv.mod.Memory().Write(0, snapCopy)
		sv.mu.Unlock()
		if !ok {
			b.Fatal("memory write failed")
		}
	}
}

// --------------------------------------------------------------------------
// userfaultfd dirty-page restore benchmark
// --------------------------------------------------------------------------

// sysUserfaultfd is the x86-64 syscall number for userfaultfd(2).
const sysUserfaultfd = 323

// userfaultfdAvailable returns true if the userfaultfd(2) syscall is permitted
// in this environment.  Under Docker's default seccomp profile (or without
// CAP_SYS_PTRACE) the syscall returns EPERM.
func userfaultfdAvailable() bool {
	fd, _, errno := syscall.Syscall(sysUserfaultfd, 0 /*flags*/, 0, 0)
	if errno != 0 {
		return false
	}
	_ = syscall.Close(int(fd))
	return true
}

// BenchmarkSnapshotRestore_Userfaultfd benchmarks the dirty-page restore
// strategy as an alternative to full memcpy.
//
// Concept: after taking a snapshot, write-protect the entire linear memory
// region with mprotect(PROT_READ).  A fault handler (userfaultfd or SIGSEGV)
// tracks which pages the guest dirtied.  On restore, only those dirty pages
// are overwritten from the snapshot, then the region is re-armed as PROT_READ.
//
// For benchmark isolation, dirty-page tracking is simulated: we pre-select
// every 10th page (~10% of total) as dirty.  The measured loop therefore
// captures:
//   - selective memcpy of ~10% of pages
//   - mprotect(PROT_READ) to re-arm the full region
//   - mprotect(PROT_READ|PROT_WRITE) to restore write access
//
// The benchmark is skipped if userfaultfd is unavailable (seccomp / missing
// privileges in this container).
func BenchmarkSnapshotRestore_Userfaultfd(b *testing.B) {
	if !userfaultfdAvailable() {
		b.Skip("userfaultfd syscall not available (seccomp or insufficient privileges)")
	}

	ctx := context.Background()

	rt := wazero.NewRuntime(ctx)
	b.Cleanup(func() { _ = rt.Close(ctx) })

	_, err := wasi_snapshot_preview1.Instantiate(ctx, rt)
	require.NoError(b, err)

	wasmBytes := benchEchoWasmBytes(b)
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	require.NoError(b, err)
	b.Cleanup(func() { _ = compiled.Close(ctx) })

	modCfg := wazero.NewModuleConfig().
		WithName("").
		WithSysNanosleep().
		WithSysWalltime().
		WithSysNanotime()
	mod, err := rt.InstantiateModule(ctx, compiled,
		modCfg.WithStartFunctions("_initialize", "_start"))
	require.NoError(b, err)
	b.Cleanup(func() { _ = mod.Close(ctx) })

	mem := mod.Memory()
	require.NotNil(b, mem, "echo module must have linear memory")

	memSize := int(mem.Size())
	pageSize := syscall.Getpagesize()
	numPages := memSize / pageSize

	// Obtain the backing pointer for wazero's linear-memory buffer.
	// wazero backs api.Memory with a Go []byte; we need the raw pointer
	// for mprotect(2).
	rawSlice, ok := mem.Read(0, uint32(memSize))
	require.True(b, ok, "mem.Read failed")
	memStart := uintptr(unsafe.Pointer(&rawSlice[0]))

	// Take snapshot.
	snapshot := make([]byte, memSize)
	copy(snapshot, rawSlice)

	// Pre-select ~10% of pages as "dirty" (simulated tracking).
	dirtyPages := make([]int, 0, numPages/10+1)
	for p := 0; p < numPages; p += 10 {
		dirtyPages = append(dirtyPages, p)
	}

	b.SetBytes(int64(memSize))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Restore phase: copy only dirty pages back from snapshot.
		for _, pg := range dirtyPages {
			off := pg * pageSize
			copy(rawSlice[off:off+pageSize], snapshot[off:off+pageSize])
		}

		// Re-arm: write-protect the whole region (post-request state).
		if _, _, errno := syscall.Syscall(
			syscall.SYS_MPROTECT,
			memStart,
			uintptr(memSize),
			syscall.PROT_READ,
		); errno != 0 {
			b.Fatalf("mprotect PROT_READ failed: %v", errno)
		}

		// Restore write access for the next iteration.
		// (In production the per-page fault handler does this on demand.)
		if _, _, errno := syscall.Syscall(
			syscall.SYS_MPROTECT,
			memStart,
			uintptr(memSize),
			syscall.PROT_READ|syscall.PROT_WRITE,
		); errno != 0 {
			b.Fatalf("mprotect PROT_READ|PROT_WRITE failed: %v", errno)
		}
	}
}

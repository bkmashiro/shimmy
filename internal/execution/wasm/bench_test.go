//go:build linux

package wasm

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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
// The echo.wasm fixture stores its bump-allocator pointer in linear memory
// (offset 0, 4-byte LE i32).  restoreSnapshot() snapshots/restores linear
// memory, so the heap pointer is correctly reset after every request.
// No dispatcher recreation is needed during benchmarks.
// --------------------------------------------------------------------------

// BenchmarkDispatcher_Send_Pool1 measures the per-request round-trip cost
// through a WASM dispatcher with exactly one module instance (no pool
// contention): alloc → memory write → evaluate → response parse →
// snapshot restore.
func BenchmarkDispatcher_Send_Pool1(b *testing.B) {
	ctx := context.Background()
	modPath := benchEchoModulePath(b)

	data := map[string]any{"i": 1}

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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
func BenchmarkDispatcher_Send_PoolN(b *testing.B) {
	ctx := context.Background()
	n := runtime.NumCPU()
	modPath := benchEchoModulePath(b)
	data := map[string]any{"i": 1}

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
	b.Cleanup(func() { _ = d.Shutdown(ctx) })

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := d.Send(ctx, "eval", data)
			if err != nil {
				b.Errorf("Send error: %v", err)
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

	sv := newWasmSupervisor(rt, compiled, modCfg, 5*time.Second, false, zap.NewNop())
	require.NoError(b, sv.Start(ctx))
	b.Cleanup(func() { _ = sv.Shutdown(ctx) })

	sv.mu.Lock()
	memSize := sv.mod.Memory().Size()
	// Read the current linear memory to build a local snapshot copy for the
	// benchmark — sv.snapshot was removed when the SnapshotStrategy interface
	// was introduced; the benchmark drives mem.Write directly.
	rawMem, ok2 := sv.mod.Memory().Read(0, memSize)
	require.True(b, ok2, "read linear memory for bench snapshot")
	snapCopy := make([]byte, len(rawMem))
	copy(snapCopy, rawMem)
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

// --------------------------------------------------------------------------
// Realistic-size snapshot/restore benchmarks
// --------------------------------------------------------------------------

// BenchmarkSnapshotRestore_FullMemcpy_3MB benchmarks FullMemcpyStrategy at a
// realistic module size (3 MB ≈ a compiled Go WASM module).  The echo.wasm
// fixture is only 64 KB; this benchmark creates an in-memory WASM module that
// allocates ~3 MB of linear memory so the restore cost reflects real-world
// Go-based eval functions.
//
// The WAT module simply declares 48 pages (48 × 64 KB = 3 MB) of linear
// memory and exports a memory view.  We use wazero to instantiate it and then
// drive FullMemcpyStrategy.Restore directly.
func BenchmarkSnapshotRestore_FullMemcpy_3MB(b *testing.B) {
	const targetMB = 3
	const wasm64KBPages = targetMB * 1024 / 64 // 48 pages → 3 MB

	// Minimal binary-encoded WASM module:
	//   (module (memory (export "mem") <pages>))
	// Encoded in the WASM binary format (magic + version + memory section).
	wasmBin := buildMinimalMemoryModule(b, wasm64KBPages)

	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	b.Cleanup(func() { _ = rt.Close(ctx) })

	compiled, err := rt.CompileModule(ctx, wasmBin)
	require.NoError(b, err, "compile minimal 3MB module")
	b.Cleanup(func() { _ = compiled.Close(ctx) })

	modCfg := wazero.NewModuleConfig().WithName("")
	mod, err := rt.InstantiateModule(ctx, compiled, modCfg)
	require.NoError(b, err, "instantiate minimal 3MB module")
	b.Cleanup(func() { _ = mod.Close(ctx) })

	mem := mod.Memory()
	require.NotNil(b, mem, "module must have linear memory")

	strategy := NewFullMemcpyStrategy()
	require.NoError(b, strategy.Take(mem), "take snapshot of 3MB memory")

	b.SetBytes(int64(mem.Size()))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := strategy.Restore(mem); err != nil {
			b.Fatalf("Restore failed: %v", err)
		}
	}
}

// BenchmarkSnapshotRestore_Uffd_3MB benchmarks UffdStrategy at a realistic
// module size (3 MB). Only dirty pages (~10% simulated) are restored.
// The benchmark is skipped if uffd+WP is unavailable (Docker seccomp).
//
// Compare with BenchmarkSnapshotRestore_FullMemcpy_3MB to see the advantage
// of dirty-page tracking: only ~10% of pages are written back per iteration.
func BenchmarkSnapshotRestore_Uffd_3MB(b *testing.B) {
	if !userfaultfdAvailable() {
		b.Skip("userfaultfd syscall not available (seccomp or insufficient privileges)")
	}

	const targetMB = 3
	const wasm64KBPages = targetMB * 1024 / 64 // 48 pages → 3 MB

	wasmBin := buildMinimalMemoryModule(b, wasm64KBPages)

	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	b.Cleanup(func() { _ = rt.Close(ctx) })

	compiled, err := rt.CompileModule(ctx, wasmBin)
	require.NoError(b, err, "compile minimal 3MB module")
	b.Cleanup(func() { _ = compiled.Close(ctx) })

	modCfg := wazero.NewModuleConfig().WithName("")
	mod, err := rt.InstantiateModule(ctx, compiled, modCfg)
	require.NoError(b, err, "instantiate minimal 3MB module")
	b.Cleanup(func() { _ = mod.Close(ctx) })

	mem := mod.Memory()
	require.NotNil(b, mem, "module must have linear memory")

	strategy, err := NewUffdStrategy(mem)
	if err != nil {
		b.Skipf("NewUffdStrategy failed (uffd+WP unavailable): %v", err)
	}
	b.Cleanup(func() { _ = strategy.Close() })

	require.NoError(b, strategy.Take(mem), "take initial snapshot")

	memSize := int(mem.Size())
	pageSize := strategy.pageSize
	numPages := memSize / pageSize

	// Pre-select ~10% of pages to simulate as dirty each iteration.
	dirtyPages := make([]int, 0, numPages/10+1)
	for p := 0; p < numPages; p += 10 {
		dirtyPages = append(dirtyPages, p)
	}

	b.SetBytes(int64(memSize))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Simulate writes on dirty pages: disarm WP, write, faultLoop re-arms.
		// For benchmark isolation we directly mark pages dirty and do the write.
		strategy.mu.Lock()
		for _, pg := range dirtyPages {
			if pg < len(strategy.dirty) {
				strategy.dirty[pg] = true
			}
		}
		strategy.mu.Unlock()

		// Disarm WP for dirty pages so we can write without faults.
		for _, pg := range dirtyPages {
			off := uint32(pg * pageSize)
			end := off + uint32(pageSize)
			if end > uint32(memSize) {
				end = uint32(memSize)
			}
			wp := uffdioWPStrategy{
				uffdioRangeStrategy: uffdioRangeStrategy{
					start: uint64(uintptr(strategy.basePtr)) + uint64(off),
					len:   uint64(end - off),
				},
				mode: 0, // disarm
			}
			ioctlUffd(uintptr(strategy.uffdFd), ioctlUffdioWPStrategy, uintptr(unsafe.Pointer(&wp))) //nolint:errcheck
			// Write zeros to the page to simulate guest activity.
			mem.Write(off, make([]byte, end-off)) //nolint:errcheck
		}

		// Restore — only dirty pages.
		if err := strategy.Restore(mem); err != nil {
			b.Fatalf("Restore failed: %v", err)
		}
	}
}

// buildMinimalMemoryModule constructs a valid WASM binary that declares
// `pages` pages of linear memory and nothing else. This lets us benchmark
// snapshot/restore at arbitrary memory sizes without a real module.
//
// Binary layout (WASM spec §5):
//   \0asm (magic) + version (1) + memory section
func buildMinimalMemoryModule(b *testing.B, pages int) []byte {
	b.Helper()

	// LEB128-encode a uint32.
	leb128 := func(v uint32) []byte {
		var buf []byte
		for {
			b := byte(v & 0x7f)
			v >>= 7
			if v != 0 {
				b |= 0x80
			}
			buf = append(buf, b)
			if v == 0 {
				break
			}
		}
		return buf
	}

	// Memory section payload: count=1, limits type=0x00 (min only), min=pages
	memPayload := append([]byte{0x01, 0x00}, leb128(uint32(pages))...)

	// Section: id=5 (memory), size=len(payload), payload
	memSec := append([]byte{0x05}, append(leb128(uint32(len(memPayload))), memPayload...)...)

	// Full module: magic + version + memory section
	module := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	module = append(module, memSec...)
	return module
}

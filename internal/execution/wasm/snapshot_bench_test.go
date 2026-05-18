//go:build linux

package wasm

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Snapshot Strategy Benchmark Suite
//
// Dimensions measured:
//
//  1. BenchmarkStrategy    — realistic per-request latency: strategy × workload
//  2. BenchmarkRestoreNPages — precise dirty-page-count effect on Restore cost
//  3. BenchmarkPool        — concurrent throughput: strategy × pool size N
//  4. BenchmarkTakeOnly    — isolate Take() (snapshot copy) cost
//  5. BenchmarkRestoreOnly — isolate Restore() with zero dirty pages
//
// Run examples:
//
//	PYTHON_REACTOR_WASM=~/bench/python-reactor.wasm CGO_ENABLED=1 \
//	  go test -bench=BenchmarkStrategy -benchtime=30s -benchmem \
//	  ./internal/execution/wasm/ | tee bench-strategy.txt
//
//	PYTHON_REACTOR_WASM=~/bench/python-reactor.wasm CGO_ENABLED=1 \
//	  go test -bench='BenchmarkPool|BenchmarkRestoreNPages' \
//	  -benchtime=20s -benchmem \
//	  ./internal/execution/wasm/ | tee bench-pool.txt
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Workload scripts
//
// Python bytearray(n) for n < 256KB goes through dlmalloc's heap (sbrk/brk
// path inside WASM linear memory) rather than mmap/memory.grow.  The heap
// metadata is fully reset by snapshot restore, so no cumulative growth occurs.
//
// Allocations ≥ 256KB (dlmalloc's DEFAULT_MMAP_THRESHOLD) call memory.grow,
// which is irreversible.  Those must NOT be used in a tight benchmark loop.
//
//	sparse  — minimal writes  (~50 dirty pages  — interpreter overhead only)
//	medium  — 128 KB bytearray (~82 dirty pages  — 32 new pages + overhead)
//	dense   — 192 KB bytearray (~98 dirty pages  — 48 new pages + overhead)
//
// For finer control over dirty-page count, see BenchmarkRestoreNPages, which
// uses direct Go writes to the WASM linear memory instead of Python.
// ---------------------------------------------------------------------------

const (
	sparseScript = `
def evaluation_function(response, answer, params=None):
    try:
        ok = float(response) == float(answer)
    except Exception:
        ok = False
    return {"is_correct": ok, "feedback": "ok" if ok else "no"}
`
	// mediumScript: 128 KB bytearray — well below dlmalloc's 256 KB mmap
	// threshold, so memory stays within the 14 MB snapshot region.
	mediumScript = `
def evaluation_function(response, answer, params=None):
    n = 32
    buf = bytearray(n * 4096)  # 128 KB — uses heap, not mmap
    for i in range(n):
        buf[i * 4096] = i & 0xFF
    return {"is_correct": True, "feedback": str(n)}
`
	// denseScript: 192 KB bytearray — still below the 256 KB mmap threshold.
	denseScript = `
def evaluation_function(response, answer, params=None):
    n = 48
    buf = bytearray(n * 4096)  # 192 KB — uses heap, not mmap
    for i in range(n):
        buf[i * 4096] = i & 0xFF
    return {"is_correct": True, "feedback": str(n)}
`
)

type workloadCase struct {
	name   string
	script string
	input  string
}

var allWorkloads = []workloadCase{
	{"sparse", sparseScript, `{"response":"42","answer":"42"}`},
	{"medium", mediumScript, `{"response":"","answer":""}`},
	{"dense", denseScript, `{"response":"","answer":""}`},
}

var allStrategies = []string{"memcpy", "soft-dirty", "mprotect", "uffd"}

// concurrentSafe reports whether a strategy supports N>1 concurrent runners.
//
// mprotect:   global C SIGSEGV handler tracks a single [base, base+size) region;
//             a second runner overwrites g_base/g_size and corrupts tracking.
// soft-dirty: /proc/self/clear_refs resets ALL PTEs in the process;
//             concurrent runners destroy each other's dirty-page information.
func concurrentSafe(mode string) bool {
	return mode == "memcpy" || mode == "uffd"
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func wasmPathOrSkip(b testing.TB) string {
	b.Helper()
	p := os.Getenv("PYTHON_REACTOR_WASM")
	if p == "" {
		b.Skip("PYTHON_REACTOR_WASM not set")
	}
	return p
}

func newBenchRunner(b testing.TB, wasmPath, mode string) *ReactorPythonRunner {
	b.Helper()
	cfg := Config{
		Timeout:        60 * time.Second,
		MaxMemoryPages: 8192,
		SnapshotMode:   mode,
	}
	r := NewReactorPythonRunner(wasmPath, cfg, zap.NewNop())
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	if err := r.Init(ctx); err != nil {
		b.Fatalf("Init(mode=%s): %v", mode, err)
	}
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = r.Shutdown(ctx)
	})
	return r
}

// ---------------------------------------------------------------------------
// BenchmarkStrategy — per-request latency: strategy × workload
//
// Measures the full Restore() + py_exec() round-trip time under realistic
// Python workloads with varying heap write pressure.
// ---------------------------------------------------------------------------

func BenchmarkStrategy(b *testing.B) {
	wasmPath := wasmPathOrSkip(b)

	for _, st := range allStrategies {
		for _, wl := range allWorkloads {
			b.Run(st+"/"+wl.name, func(b *testing.B) {
				r := newBenchRunner(b, wasmPath, st)
				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					_, err := r.SendRequest(ctx, wl.script, "eval", wl.input)
					cancel()
					if err != nil {
						b.Fatalf("SendRequest[%d]: %v", i, err)
					}
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// BenchmarkRestoreNPages — precise dirty-page-count effect on Restore cost
//
// Instead of using Python to dirty memory (which risks OOM via memory.grow),
// this benchmark writes directly to WASM linear memory from Go to dirty
// exactly N 4 KB pages, then times strategy.Restore().
//
// For mprotect: writing to PROT_READ memory triggers the C SIGSEGV handler,
// which marks the page dirty and lifts write-protection — identical to what
// happens when WASM guest code writes during py_exec.
//
// For soft-dirty: Go writes set the soft-dirty bit in the kernel PTE, which
// is exactly what pagemap tracks.
//
// This lets us plot Restore cost as a function of dirty page count without
// any Python involvement, enabling a clean strategy comparison curve.
// ---------------------------------------------------------------------------

func BenchmarkRestoreNPages(b *testing.B) {
	wasmPath := wasmPathOrSkip(b)
	pageSize := syscall.Getpagesize()

	// Dirty page counts to test. 3500 ≈ full 14 MB / 4 KB.
	dirtyCounts := []int{0, 16, 64, 256, 512, 1024, 2048, 3500}

	for _, st := range allStrategies {
		for _, nDirty := range dirtyCounts {
			b.Run(fmt.Sprintf("%s/dirty%d", st, nDirty), func(b *testing.B) {
				r := newBenchRunner(b, wasmPath, st)
				mem := r.mod.Memory()

				// Get the base pointer of the WASM linear memory backing slice.
				buf, ok := mem.Read(0, mem.Size())
				if !ok {
					b.Fatal("mem.Read failed")
				}
				basePtr := unsafe.Pointer(unsafe.SliceData(buf))
				totalPages := int(mem.Size()) / pageSize
				if nDirty > totalPages {
					b.Skipf("nDirty=%d > totalPages=%d", nDirty, totalPages)
				}

				// mprotect: mprotect_ro(14MB) must TLB-shootdown all vCPUs for
				// each written page. In VMs (Hyper-V/Azure) the hypervisor
				// serialises cross-vCPU IPIs, so shootdown cost scales with
				// dirty-page count. timeout=3600s gives dirty1024..3500 room
				// to complete even if each iteration takes several minutes.

				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					b.StopTimer()
					// Dirty exactly nDirty pages before each Restore().
					//
					// Strategy-specific approach:
					//   mprotect:   forceDirtyNPages — ONE mprotect(RW) on whole
					//               region + N bitmap bits set. Avoids both rapid
					//               Go SIGSEGV (runtime signal-dispatch crash) AND
					//               N per-page mprotect calls (VMA fragmentation
					//               causes O(N²) kernel merge work → 10-min hang).
					//   soft-dirty: plain Go writes — sets kernel PTE soft-dirty
					//               bit, same as WASM guest writes would.
					//   memcpy/uffd: writes are irrelevant (Restore copies all
					//               pages regardless), but we write anyway to keep
					//               the loop body identical across strategies.
					switch ms := r.strategy.(type) {
					case *MprotectStrategy:
						ms.forceDirtyNPages(nDirty)
					default:
						raw := unsafe.Slice((*byte)(basePtr), nDirty*pageSize)
						for pg := 0; pg < nDirty; pg++ {
							raw[pg*pageSize] ^= 0xFF
						}
					}
					b.StartTimer()

					if err := r.strategy.Restore(mem); err != nil {
						b.Fatalf("Restore: %v", err)
					}
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// BenchmarkPool — concurrent throughput: strategy × pool size N
//
// Models a production deployment with N independent ReactorPythonRunner
// instances serving concurrent requests via a channel-based pool.
// True concurrency is bounded by N, not GOMAXPROCS.
//
// mprotect and soft-dirty are skipped for N>1 (process-wide global state).
// Expected: memcpy / uffd scale near-linearly; mprotect / soft-dirty: N=1 only.
// ---------------------------------------------------------------------------

func BenchmarkPool(b *testing.B) {
	wasmPath := wasmPathOrSkip(b)
	poolSizes := []int{1, 2, 4, 8}

	for _, st := range allStrategies {
		for _, N := range poolSizes {
			b.Run(fmt.Sprintf("%s/N%d", st, N), func(b *testing.B) {
				if N > 1 && !concurrentSafe(st) {
					b.Skipf("strategy %s uses process-wide global state — not safe for N>1 runners", st)
				}

				runners := make([]*ReactorPythonRunner, N)
				for i := range runners {
					cfg := Config{
						Timeout:        60 * time.Second,
						MaxMemoryPages: 8192,
						SnapshotMode:   st,
					}
					r := NewReactorPythonRunner(wasmPath, cfg, zap.NewNop())
					ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
					if err := r.Init(ctx); err != nil {
						cancel()
						b.Fatalf("Init runner[%d] mode=%s: %v", i, st, err)
					}
					cancel()
					runners[i] = r
				}
				b.Cleanup(func() {
					for _, r := range runners {
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						_ = r.Shutdown(ctx)
						cancel()
					}
				})

				pool := make(chan *ReactorPythonRunner, N)
				for _, r := range runners {
					pool <- r
				}

				script := sparseScript
				input := `{"response":"42","answer":"42"}`

				b.ReportAllocs()
				b.ResetTimer()

				var mu sync.Mutex
				var firstErr error

				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						r := <-pool
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						_, err := r.SendRequest(ctx, script, "eval", input)
						cancel()
						pool <- r
						if err != nil {
							mu.Lock()
							if firstErr == nil {
								firstErr = err
							}
							mu.Unlock()
						}
					}
				})

				if firstErr != nil {
					b.Fatalf("SendRequest: %v", firstErr)
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// BenchmarkTakeOnly — isolate Take() (snapshot copy) cost
//
// All strategies copy the full linear memory during Take(), plus any
// strategy-specific bookkeeping (mprotect: write-protect region;
// soft-dirty: clear_refs write).
// ---------------------------------------------------------------------------

func BenchmarkTakeOnly(b *testing.B) {
	wasmPath := wasmPathOrSkip(b)
	for _, st := range allStrategies {
		b.Run(st, func(b *testing.B) {
			r := newBenchRunner(b, wasmPath, st)
			mem := r.mod.Memory()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := r.strategy.Take(mem); err != nil {
					b.Fatalf("Take: %v", err)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BenchmarkRestoreOnly — isolate Restore() cost with zero dirty pages
//
// Measures the minimum Restore overhead when no pages were written since
// the last Take (no dirty pages to copy back).
//
// mprotect: only calls mprotect_ro(whole region) + clear_dirty_bitmap — cheap.
// soft-dirty: reads pagemap (bulk ReadAt), finds no dirty bits — cheap.
// memcpy / uffd: copies all 14 MB regardless — expensive.
// ---------------------------------------------------------------------------

func BenchmarkRestoreOnly(b *testing.B) {
	wasmPath := wasmPathOrSkip(b)
	for _, st := range allStrategies {
		b.Run(st, func(b *testing.B) {
			r := newBenchRunner(b, wasmPath, st)
			mem := r.mod.Memory()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := r.strategy.Restore(mem); err != nil {
					b.Fatalf("Restore: %v", err)
				}
			}
		})
	}
}

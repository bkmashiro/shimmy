//go:build linux

package wasm

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Snapshot Strategy Benchmark Suite
//
// Dimensions measured:
//
//  1. BenchmarkStrategy — per-request latency: strategy × workload (dirty ratio)
//  2. BenchmarkPool     — concurrent throughput: strategy × pool size N
//
// Run all:
//
//	PYTHON_REACTOR_WASM=~/bench/python-reactor.wasm \
//	  CGO_ENABLED=1 go test -bench=BenchmarkStrategy \
//	  -benchtime=30s -benchmem ./internal/execution/wasm/ | tee bench-strategy.txt
//
//	PYTHON_REACTOR_WASM=~/bench/python-reactor.wasm \
//	  CGO_ENABLED=1 go test -bench=BenchmarkPool \
//	  -benchtime=30s -benchmem ./internal/execution/wasm/ | tee bench-pool.txt
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Workload definitions
//
// Each workload controls how many 4 KB pages CPython writes during one
// py_exec() call.  After strategy.Take() the WASM linear memory is write-
// protected (mprotect) or soft-dirty-cleared; writes from Python set the
// dirty state that Restore() must undo.
//
// Dirty-page counts are approximate: CPython's allocator coalesces small
// objects, so the actual number of newly-touched pages is workload-dependent.
//
//	sparse  —  <  50 dirty pages  (minimal eval, no large allocs)
//	medium  — ~128 dirty pages  (512 KB bytearray, one write per page)
//	dense   — ~512 dirty pages  (2 MB bytearray, one write per page)
// ---------------------------------------------------------------------------

const (
	// sparseScript: equivalent to the standard eval.py — minimal heap pressure.
	sparseScript = `
def evaluation_function(response, answer, params=None):
    try:
        ok = float(response) == float(answer)
    except Exception:
        ok = False
    return {"is_correct": ok, "feedback": "ok" if ok else "no"}
`

	// mediumScript: allocates a 512 KB bytearray and writes one byte per page.
	// Expected: ~128 dirty pages beyond normal interpreter overhead.
	mediumScript = `
def evaluation_function(response, answer, params=None):
    n = 128
    buf = bytearray(n * 4096)
    for i in range(n):
        buf[i * 4096] = i & 0xFF
    return {"is_correct": True, "feedback": str(n)}
`

	// denseScript: allocates a 2 MB bytearray and writes one byte per page.
	// Expected: ~512 dirty pages beyond normal interpreter overhead.
	denseScript = `
def evaluation_function(response, answer, params=None):
    n = 512
    buf = bytearray(n * 4096)
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

// allStrategies lists the strategies under test.
// uffd is currently a memcpy fallback (wazero does not expose the raw linear-
// memory mmap address needed for UFFDIO_REGISTER_MODE_WP).
var allStrategies = []string{"memcpy", "soft-dirty", "mprotect", "uffd"}

// concurrentSafe reports whether a strategy supports N>1 concurrent runners.
// mprotect: global C signal handler + global dirty bitmap → single-instance only.
// soft-dirty: /proc/self/clear_refs resets ALL PTEs in the process → single-instance only.
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
// Each sub-benchmark measures: strategy.Restore() + py_exec() round-trip.
// This is the dominant cost for a single-runner deployment.
//
// Results interpretation:
//   - sparse workload   → favours mprotect (few dirty pages → cheap restore)
//   - dense workload    → strategies converge (more pages = more restore work)
//   - soft-dirty/dense  → soft-dirty may stay slower than memcpy due to
//     pagemap scan overhead even with bulk read
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
// BenchmarkPool — concurrent throughput: strategy × pool size N
//
// Models a production deployment where N independent ReactorPythonRunner
// instances serve concurrent requests.  A channel-based pool distributes
// requests across the N runners; true parallelism is bounded by N, not
// GOMAXPROCS.
//
// Strategies with process-wide global state (mprotect, soft-dirty) are
// skipped for N>1 with a documented reason:
//
//	mprotect:   global C SIGSEGV handler tracks only one [base, base+size)
//	            region; a second runner would corrupt the dirty bitmap.
//	soft-dirty: /proc/self/clear_refs resets ALL PTEs, so concurrent
//	            runners would lose each other's dirty-page information.
//
// Expected outcome:
//   - memcpy / uffd  → near-linear throughput scaling with N
//   - mprotect       → only N=1 tested (skipped for N>1)
//   - soft-dirty     → only N=1 tested (skipped for N>1)
// ---------------------------------------------------------------------------

func BenchmarkPool(b *testing.B) {
	wasmPath := wasmPathOrSkip(b)
	poolSizes := []int{1, 2, 4, 8}

	for _, st := range allStrategies {
		for _, N := range poolSizes {
			name := fmt.Sprintf("%s/N%d", st, N)
			b.Run(name, func(b *testing.B) {
				if N > 1 && !concurrentSafe(st) {
					b.Skipf("strategy %s uses process-wide global state; not safe for N>1 concurrent runners", st)
				}

				// Initialise N runners.
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

				// Channel-based pool: limits true concurrency to N.
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
					b.Fatalf("SendRequest error: %v", firstErr)
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// BenchmarkRestoreOnly — isolate the Restore() cost from py_exec()
//
// Calls strategy.Restore(mem) in a tight loop without running Python between
// calls.  Because no pages are dirtied, this measures the minimum restore
// overhead (write-unprotect-and-re-protect for mprotect; pagemap scan for
// soft-dirty; memcpy for full strategies).
//
// Note: mprotect's Restore() with zero dirty pages is extremely cheap — it
// only calls mprotect(PROT_RO) on the region and clears the bitmap.
// The per-request benchmark (BenchmarkStrategy/sparse) better reflects
// realistic cost including SIGSEGV fault handling during py_exec.
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

// ---------------------------------------------------------------------------
// BenchmarkTakeOnly — isolate the Take() (snapshot) cost
//
// Calls strategy.Take(mem) in a tight loop.  Take() always copies the full
// linear memory regardless of strategy (all strategies need a ground-truth
// snapshot).  This measures raw memcpy throughput for the 14 MB region plus
// any strategy-specific bookkeeping (mprotect PROT_RO, clear_refs write).
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

//go:build linux

package wasm

import (
	"fmt"
	"testing"
	"unsafe"

	"go.uber.org/zap"
)

// BenchmarkLargeMemoryRestore measures end-to-end snapshot Restore lifecycles
// at sizes representative of the CPython-WASI artifact and larger evaluator
// heaps. The mutation phase is excluded from timing, but dirty-page evidence is
// validated before every Restore so a fallback or tracking failure cannot
// silently produce a plausible curve.
//
// Run with -benchtime=1x: each case allocates both linear memory and a snapshot.
func BenchmarkLargeMemoryRestore(b *testing.B) {
	const wasmPageBytes = 64 * 1024
	const dirtyPageBytes = 4 * 1024
	sizesMiB := []int{128, 256, 512}
	dirtyPercents := []int{1, 10, 50}
	modes := []string{"memcpy", "soft-dirty", "uffd"}

	for _, mode := range modes {
		for _, sizeMiB := range sizesMiB {
			for _, dirtyPercent := range dirtyPercents {
				name := fmt.Sprintf("%s/%dMiB/dirty%dpc", mode, sizeMiB, dirtyPercent)
				b.Run(name, func(b *testing.B) {
					plan, err := newLargeRestoreBenchmarkPlan(sizeMiB, dirtyPercent, dirtyPageBytes)
					if err != nil {
						b.Fatalf("benchmark plan: %v", err)
					}
					pages := sizeMiB * 1024 * 1024 / wasmPageBytes
					mem := newTestWazeroMemory(b, pages)
					strategy := selectSnapshotStrategy(mode, mem, zap.NewNop())
					b.Cleanup(func() { _ = strategy.Close() })

					var dirtyCount func() (int, error)
					switch mode {
					case "memcpy":
						if _, ok := strategy.(*FullMemcpyStrategy); !ok {
							b.Fatalf("memcpy selected %T", strategy)
						}
					case "soft-dirty":
						softDirty, ok := strategy.(*SoftDirtyStrategy)
						if !ok {
							b.Fatalf("soft-dirty selected %T; refusing fallback evidence", strategy)
						}
						dirtyCount = softDirty.DirtyPageCount
					case "uffd":
						uffd, ok := strategy.(*UffdStrategy)
						if !ok {
							b.Fatalf("uffd selected %T; refusing fallback evidence", strategy)
						}
						dirtyCount = func() (int, error) { return uffd.DirtyPageCount(), nil }
					}

					if err := strategy.Take(mem); err != nil {
						b.Fatalf("Take: %v", err)
					}
					buf, ok := mem.Read(0, mem.Size())
					if !ok {
						b.Fatal("memory read failed")
					}

					lastObserved := -1
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						b.StopTimer()
						for page := 0; page < plan.requestedDirtyPages; page++ {
							buf[page*dirtyPageBytes] ^= byte(i + 1)
						}
						if dirtyCount != nil {
							observed, err := dirtyCount()
							if err != nil {
								b.Fatalf("read observed dirty pages: %v", err)
							}
							if err := validateObservedDirtyPages(mode, plan, observed); err != nil {
								b.Fatal(err)
							}
							lastObserved = observed
						}
						b.StartTimer()
						if err := strategy.Restore(mem); err != nil {
							b.Fatalf("Restore: %v", err)
						}
					}
					b.StopTimer()
					if lastObserved >= 0 {
						reportLargeRestoreMetrics(b, plan, &lastObserved, false)
					} else {
						reportLargeRestoreMetrics(b, plan, nil, false)
					}
				})
			}
		}
	}
}

// BenchmarkUffdLargeMemoryPhases isolates the operations that the end-to-end
// UFFD Restore benchmark combines. rearm-dirty-contiguous is an experimental
// comparator over the same contiguous dirty pattern; production Restore still
// uses rearm-full. Benchmark names, rather than inferred throughput, identify
// each phase, while custom metrics preserve the requested dirty extent.
func BenchmarkUffdLargeMemoryPhases(b *testing.B) {
	const sizeMiB = 512
	const dirtyPageBytes = 4 * 1024
	for _, dirtyPercent := range []int{1, 10, 50} {
		plan, err := newLargeRestoreBenchmarkPlan(sizeMiB, dirtyPercent, dirtyPageBytes)
		if err != nil {
			b.Fatalf("benchmark plan: %v", err)
		}
		dirtyPages := make([]int, plan.requestedDirtyPages)
		for page := range dirtyPages {
			dirtyPages[page] = page
		}
		ranges := newDirtyPageRanges(dirtyPages, dirtyPageBytes, int(plan.extentBytes))

		b.Run(fmt.Sprintf("copy-dirty/%dMiB/dirty%dpc", sizeMiB, dirtyPercent), func(b *testing.B) {
			strategy := newLargeUffdBenchmarkStrategy(b, sizeMiB)
			dst := unsafe.Slice((*byte)(strategy.basePtr), strategy.memSize)
			observed := plan.requestedDirtyPages
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				if err := setUffdBenchmarkWriteProtection(strategy, ranges, false); err != nil {
					b.Fatalf("disarm dirty ranges: %v", err)
				}
				for _, page := range dirtyPages {
					dst[page*dirtyPageBytes] ^= byte(i + 1)
				}
				b.StartTimer()
				for _, page := range dirtyPages {
					offset := page * dirtyPageBytes
					copy(dst[offset:offset+dirtyPageBytes], strategy.snapshot[offset:offset+dirtyPageBytes])
				}
			}
			b.StopTimer()
			reportLargeRestoreMetrics(b, plan, &observed, false)
			b.ReportMetric(float64(len(ranges)), "ranges/op")
		})

		b.Run(fmt.Sprintf("rearm-full/%dMiB/dirty%dpc", sizeMiB, dirtyPercent), func(b *testing.B) {
			strategy := newLargeUffdBenchmarkStrategy(b, sizeMiB)
			observed := plan.requestedDirtyPages
			b.ReportAllocs()
			fullRange := []dirtyPageRange{{offset: 0, length: int(plan.extentBytes)}}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				if err := setUffdBenchmarkWriteProtection(strategy, ranges, false); err != nil {
					b.Fatalf("disarm dirty ranges: %v", err)
				}
				b.StartTimer()
				if err := setUffdBenchmarkWriteProtection(strategy, fullRange, true); err != nil {
					b.Fatalf("re-arm full range: %v", err)
				}
			}
			b.StopTimer()
			reportLargeRestoreMetrics(b, plan, &observed, false)
			b.ReportMetric(1, "ranges/op")
		})

		b.Run(fmt.Sprintf("rearm-dirty-contiguous/%dMiB/dirty%dpc", sizeMiB, dirtyPercent), func(b *testing.B) {
			strategy := newLargeUffdBenchmarkStrategy(b, sizeMiB)
			observed := plan.requestedDirtyPages
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				if err := setUffdBenchmarkWriteProtection(strategy, ranges, false); err != nil {
					b.Fatalf("disarm dirty ranges: %v", err)
				}
				b.StartTimer()
				if err := setUffdBenchmarkWriteProtection(strategy, ranges, true); err != nil {
					b.Fatalf("re-arm dirty ranges: %v", err)
				}
			}
			b.StopTimer()
			reportLargeRestoreMetrics(b, plan, &observed, false)
			b.ReportMetric(float64(len(ranges)), "ranges/op")
		})
	}
}

func newLargeUffdBenchmarkStrategy(b *testing.B, sizeMiB int) *UffdStrategy {
	b.Helper()
	const wasmPageBytes = 64 * 1024
	pages := sizeMiB * 1024 * 1024 / wasmPageBytes
	mem := newTestWazeroMemory(b, pages)
	strategy, err := NewUffdStrategy(mem)
	if err != nil {
		b.Fatalf("NewUffdStrategy: %v", err)
	}
	b.Cleanup(func() { _ = strategy.Close() })
	if err := strategy.Take(mem); err != nil {
		b.Fatalf("Take: %v", err)
	}
	return strategy
}

func setUffdBenchmarkWriteProtection(strategy *UffdStrategy, ranges []dirtyPageRange, enabled bool) error {
	mode := uint64(0)
	if enabled {
		mode = uffdioWPModeWP
	}
	for _, dirtyRange := range ranges {
		wp := uffdioWPStrategy{
			uffdioRangeStrategy: uffdioRangeStrategy{
				start: uint64(uintptr(strategy.basePtr)) + uint64(dirtyRange.offset),
				len:   uint64(dirtyRange.length),
			},
			mode: mode,
		}
		if err := ioctlUffd(uintptr(strategy.uffdFd), ioctlUffdioWPStrategy, uintptr(unsafe.Pointer(&wp))); err != 0 {
			return fmt.Errorf("write-protect offset=%d length=%d enabled=%t: %w", dirtyRange.offset, dirtyRange.length, enabled, err)
		}
	}
	return nil
}

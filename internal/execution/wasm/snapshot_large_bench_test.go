//go:build linux

package wasm

import (
	"fmt"
	"testing"

	"go.uber.org/zap"
)

// BenchmarkLargeMemoryRestore measures current snapshot lifecycles at sizes
// representative of the CPython-WASI artifact and larger evaluator heaps.
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
					pages := sizeMiB * 1024 * 1024 / wasmPageBytes
					mem := newTestWazeroMemory(b, pages)
					strategy := selectSnapshotStrategy(mode, mem, zap.NewNop())
					b.Cleanup(func() { _ = strategy.Close() })
					if err := strategy.Take(mem); err != nil {
						b.Fatalf("Take: %v", err)
					}
					if _, fallback := strategy.(*FullMemcpyStrategy); fallback && mode != "memcpy" {
						b.ReportMetric(1, "fallback")
					} else {
						b.ReportMetric(0, "fallback")
					}

					totalDirtyPages := sizeMiB * 1024 * 1024 / dirtyPageBytes
					dirtyPages := totalDirtyPages * dirtyPercent / 100
					buf, ok := mem.Read(0, mem.Size())
					if !ok {
						b.Fatal("memory read failed")
					}
					b.SetBytes(int64(sizeMiB * 1024 * 1024))
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						b.StopTimer()
						for page := 0; page < dirtyPages; page++ {
							buf[page*dirtyPageBytes] ^= byte(i + 1)
						}
						b.StartTimer()
						if err := strategy.Restore(mem); err != nil {
							b.Fatalf("Restore: %v", err)
						}
					}
				})
			}
		}
	}
}

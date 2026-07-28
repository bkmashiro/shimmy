//go:build linux

package wasm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const cowEvidenceMemoryBytes = 64 * 1024 * 1024

func TestCowLinuxMechanismEvidence(t *testing.T) {
	evidencePath := os.Getenv("COW_MECHANISM_EVIDENCE_PATH")
	if evidencePath == "" {
		t.Skip("COW_MECHANISM_EVIDENCE_PATH not set")
	}

	coordinator := newCowImageCoordinator()
	memories := make([]*cowLinearMemory, 0, 4)
	for range 4 {
		memory, err := newCowLinearMemory(cowEvidenceMemoryBytes)
		require.NoError(t, err)
		buf := memory.Reallocate(cowEvidenceMemoryBytes)
		fillCowEvidencePattern(buf)
		require.NoError(t, coordinator.PublishOrAttach(memory))
		memories = append(memories, memory)
	}
	t.Cleanup(func() {
		for _, memory := range memories {
			memory.Free()
			require.NoError(t, memory.Release())
		}
		require.NoError(t, coordinator.Close())
	})

	imageID := coordinator.ImageID()
	require.NotEmpty(t, imageID)
	for _, memory := range memories {
		require.Equal(t, imageID, digestBytes(memory.Bytes()))
	}
	mappingCount, err := countCowImageMappings()
	require.NoError(t, err)
	require.GreaterOrEqual(t, mappingCount, len(memories), "all instances must map the same named memfd")

	cowCases := make([]map[string]any, 0, 3)
	fullCopyCases := make([]map[string]any, 0, 3)
	for _, dirtyPercent := range []int{1, 10, 50} {
		cowCase, err := measureCowEvidenceCase(memories, imageID, dirtyPercent)
		require.NoError(t, err)
		cowCases = append(cowCases, cowCase)
		fullCopyCases = append(fullCopyCases, measureFullCopyEvidenceCase(dirtyPercent))
	}

	kernelBytes, _ := os.ReadFile("/proc/version")
	evidence := map[string]any{
		"scope":                  "controlled reset and Linux VM accounting diagnostic; not end-to-end or production performance",
		"baseline_bytes":         cowEvidenceMemoryBytes,
		"instances":              len(memories),
		"canonical_image_sha256": imageID,
		"memfd_private_mappings": mappingCount,
		"dirty_percents":         []int{1, 10, 50},
		"go_version":             runtime.Version(),
		"wazero_version":         "v1.11.0",
		"platform":               runtime.GOOS + "/" + runtime.GOARCH,
		"kernel":                 strings.TrimSpace(string(kernelBytes)),
		"cow":                    cowCases,
		"full_copy":              fullCopyCases,
		"uffd":                   measureUffdEvidence(t),
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(evidencePath, append(encoded, '\n'), 0o644))
	t.Logf("COW mechanism evidence: %s", encoded)
}

func fillCowEvidencePattern(buf []byte) {
	pageSize := unix.Getpagesize()
	for offset := 0; offset < len(buf); offset += pageSize {
		buf[offset] = byte((offset/pageSize)%251 + 1)
	}
}

func digestBytes(buf []byte) string {
	digest := sha256.Sum256(buf)
	return hex.EncodeToString(digest[:])
}

func measureCowEvidenceCase(memories []*cowLinearMemory, imageID string, dirtyPercent int) (map[string]any, error) {
	pageSize := unix.Getpagesize()
	totalPages := cowEvidenceMemoryBytes / pageSize
	dirtyPages := totalPages * dirtyPercent / 100
	if dirtyPages < 1 {
		dirtyPages = 1
	}
	before, err := readSmapsRollupKB()
	if err != nil {
		return nil, err
	}
	minorBefore, err := minorFaults()
	if err != nil {
		return nil, err
	}
	for instance, memory := range memories {
		buf := memory.Bytes()
		for page := 0; page < dirtyPages; page++ {
			buf[page*pageSize] ^= byte(instance + 1)
		}
	}
	dirty, err := readSmapsRollupKB()
	if err != nil {
		return nil, err
	}
	started := time.Now()
	for _, memory := range memories {
		if err := memory.Reset(); err != nil {
			return nil, err
		}
	}
	resetDuration := time.Since(started)
	for _, memory := range memories {
		if digestBytes(memory.Bytes()) != imageID {
			return nil, fmt.Errorf("COW reset digest mismatch at dirty_percent=%d", dirtyPercent)
		}
	}
	after, err := readSmapsRollupKB()
	if err != nil {
		return nil, err
	}
	minorAfter, err := minorFaults()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"dirty_percent":          dirtyPercent,
		"requested_dirty_pages":  dirtyPages * len(memories),
		"reset_nanoseconds":      resetDuration.Nanoseconds(),
		"minor_faults_delta":     minorAfter - minorBefore,
		"smaps_before_kb":        before,
		"smaps_dirty_kb":         dirty,
		"smaps_reset_refault_kb": after,
	}, nil
}

func measureFullCopyEvidenceCase(dirtyPercent int) map[string]any {
	pageSize := unix.Getpagesize()
	totalPages := cowEvidenceMemoryBytes / pageSize
	dirtyPages := totalPages * dirtyPercent / 100
	if dirtyPages < 1 {
		dirtyPages = 1
	}
	baseline := make([]byte, cowEvidenceMemoryBytes)
	fillCowEvidencePattern(baseline)
	live := append([]byte(nil), baseline...)
	for page := 0; page < dirtyPages; page++ {
		live[page*pageSize] ^= 0xff
	}
	started := time.Now()
	copy(live, baseline)
	duration := time.Since(started)
	return map[string]any{
		"dirty_percent":         dirtyPercent,
		"requested_dirty_pages": dirtyPages,
		"restore_nanoseconds":   duration.Nanoseconds(),
		"digest_restored":       digestBytes(live) == digestBytes(baseline),
	}
}

func measureUffdEvidence(t *testing.T) map[string]any {
	t.Helper()
	if !UffdAvailable() {
		return map[string]any{"available": false, "reason": "userfaultfd write-protect unavailable on runner"}
	}
	const wasmPageBytes = 64 * 1024
	mem := newTestWazeroMemory(t, cowEvidenceMemoryBytes/wasmPageBytes)
	strategy, err := NewUffdStrategy(mem)
	if err != nil {
		return map[string]any{"available": false, "reason": err.Error()}
	}
	defer func() { require.NoError(t, strategy.Close()) }()
	if err := strategy.Take(mem); err != nil {
		return map[string]any{"available": false, "reason": err.Error()}
	}
	buf, ok := mem.Read(0, mem.Size())
	if !ok {
		return map[string]any{"available": false, "reason": "unable to read test memory"}
	}
	pageSize := unix.Getpagesize()
	cases := make([]map[string]any, 0, 3)
	for _, dirtyPercent := range []int{1, 10, 50} {
		dirtyPages := len(buf) / pageSize * dirtyPercent / 100
		for page := 0; page < dirtyPages; page++ {
			buf[page*pageSize] ^= 0xff
		}
		observed := strategy.DirtyPageCount()
		started := time.Now()
		err := strategy.Restore(mem)
		cases = append(cases, map[string]any{
			"dirty_percent":         dirtyPercent,
			"requested_dirty_pages": dirtyPages,
			"observed_dirty_pages":  observed,
			"restore_nanoseconds":   time.Since(started).Nanoseconds(),
			"error":                 errorString(err),
		})
		if err != nil {
			break
		}
	}
	return map[string]any{"available": true, "cases": cases}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func readSmapsRollupKB() (map[string]uint64, error) {
	data, err := os.ReadFile("/proc/self/smaps_rollup")
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{
		"Rss": true, "Pss": true, "Pss_Anon": true, "Pss_File": true, "Pss_Shmem": true,
		"Shared_Clean": true, "Shared_Dirty": true, "Private_Clean": true, "Private_Dirty": true,
	}
	result := make(map[string]uint64, len(wanted))
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if !wanted[key] {
			continue
		}
		value, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("parse smaps %s: %w", key, parseErr)
		}
		result[key] = value
	}
	return result, nil
}

func minorFaults() (int64, error) {
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		return 0, err
	}
	return usage.Minflt, nil
}

func countCowImageMappings() (int, error) {
	data, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		return 0, err
	}
	return strings.Count(string(data), "memfd:shimmy-cow-image"), nil
}

func BenchmarkCowVsFullCopyResetOnly(b *testing.B) {
	for _, dirtyPercent := range []int{1, 10, 50} {
		b.Run(fmt.Sprintf("cow/64MiB/dirty%dpc", dirtyPercent), func(b *testing.B) {
			coordinator := newCowImageCoordinator()
			memory, err := newCowLinearMemory(cowEvidenceMemoryBytes)
			if err != nil {
				b.Fatal(err)
			}
			buf := memory.Reallocate(cowEvidenceMemoryBytes)
			fillCowEvidencePattern(buf)
			if err := coordinator.PublishOrAttach(memory); err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				memory.Free()
				if err := memory.Release(); err != nil {
					b.Errorf("release COW memory: %v", err)
				}
				_ = coordinator.Close()
			})
			dirtyPages := len(buf) / unix.Getpagesize() * dirtyPercent / 100
			b.ReportMetric(float64(dirtyPages), "dirty-pages/op")
			b.ReportMetric(64, "extent-MiB")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				buf = memory.Bytes()
				for page := 0; page < dirtyPages; page++ {
					buf[page*unix.Getpagesize()] ^= byte(i + 1)
				}
				b.StartTimer()
				if err := memory.Reset(); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(fmt.Sprintf("full-copy/64MiB/dirty%dpc", dirtyPercent), func(b *testing.B) {
			baseline := make([]byte, cowEvidenceMemoryBytes)
			fillCowEvidencePattern(baseline)
			live := append([]byte(nil), baseline...)
			dirtyPages := len(live) / unix.Getpagesize() * dirtyPercent / 100
			b.ReportMetric(float64(dirtyPages), "dirty-pages/op")
			b.ReportMetric(64, "extent-MiB")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				for page := 0; page < dirtyPages; page++ {
					live[page*unix.Getpagesize()] ^= byte(i + 1)
				}
				b.StartTimer()
				copy(live, baseline)
			}
		})
	}
}

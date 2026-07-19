package wasm

import "testing"

type capturedBenchmarkMetrics map[string]float64

func (m capturedBenchmarkMetrics) ReportMetric(value float64, unit string) {
	m[unit] = value
}

func TestLargeRestoreBenchmarkPlanReportsDirtyEvidence(t *testing.T) {
	plan, err := newLargeRestoreBenchmarkPlan(512, 1, 4096)
	if err != nil {
		t.Fatalf("newLargeRestoreBenchmarkPlan: %v", err)
	}
	if plan.totalPages != 131072 {
		t.Fatalf("totalPages = %d, want 131072", plan.totalPages)
	}
	if plan.requestedDirtyPages != 1310 {
		t.Fatalf("requestedDirtyPages = %d, want 1310", plan.requestedDirtyPages)
	}

	observed := 1310
	metrics := capturedBenchmarkMetrics{}
	reportLargeRestoreMetrics(metrics, plan, &observed, false)

	want := map[string]float64{
		"requested_pages/op": 1310,
		"observed_pages/op":  1310,
		"dirty_B/op":         1310 * 4096,
		"extent_B/op":        512 * 1024 * 1024,
		"fallback":           0,
	}
	for unit, wantValue := range want {
		if got, ok := metrics[unit]; !ok || got != wantValue {
			t.Errorf("metric %q = %v (present=%t), want %v", unit, got, ok, wantValue)
		}
	}
}

func TestLargeRestoreBenchmarkPlanRejectsInvalidInputs(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sizeMiB      int
		dirtyPercent int
		pageBytes    int
	}{
		{name: "zero size", sizeMiB: 0, dirtyPercent: 1, pageBytes: 4096},
		{name: "zero dirty rate", sizeMiB: 128, dirtyPercent: 0, pageBytes: 4096},
		{name: "dirty rate over one hundred", sizeMiB: 128, dirtyPercent: 101, pageBytes: 4096},
		{name: "zero page size", sizeMiB: 128, dirtyPercent: 1, pageBytes: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newLargeRestoreBenchmarkPlan(tc.sizeMiB, tc.dirtyPercent, tc.pageBytes); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDirtyPageRangesCoalesceContiguousPages(t *testing.T) {
	got := newDirtyPageRanges([]int{0, 1, 2, 5, 7, 8}, 4096, 9*4096)
	want := []dirtyPageRange{
		{offset: 0, length: 3 * 4096},
		{offset: 5 * 4096, length: 4096},
		{offset: 7 * 4096, length: 2 * 4096},
	}
	if len(got) != len(want) {
		t.Fatalf("range count = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("range %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

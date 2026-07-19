package wasm

import "fmt"

type largeRestoreBenchmarkPlan struct {
	extentBytes         int64
	pageBytes           int
	totalPages          int
	requestedDirtyPages int
}

type benchmarkMetricReporter interface {
	ReportMetric(value float64, unit string)
}

type dirtyPageRange struct {
	offset int
	length int
}

func newDirtyPageRanges(pages []int, pageBytes, extentBytes int) []dirtyPageRange {
	if len(pages) == 0 || pageBytes <= 0 || extentBytes <= 0 {
		return nil
	}

	ranges := make([]dirtyPageRange, 0, len(pages))
	start := pages[0]
	previous := start
	appendRange := func(first, last int) {
		offset := first * pageBytes
		end := (last + 1) * pageBytes
		if end > extentBytes {
			end = extentBytes
		}
		if offset >= 0 && offset < end {
			ranges = append(ranges, dirtyPageRange{offset: offset, length: end - offset})
		}
	}

	for _, page := range pages[1:] {
		if page == previous+1 {
			previous = page
			continue
		}
		appendRange(start, previous)
		start = page
		previous = page
	}
	appendRange(start, previous)
	return ranges
}

func newLargeRestoreBenchmarkPlan(sizeMiB, dirtyPercent, pageBytes int) (largeRestoreBenchmarkPlan, error) {
	if sizeMiB <= 0 {
		return largeRestoreBenchmarkPlan{}, fmt.Errorf("size MiB must be positive")
	}
	if dirtyPercent <= 0 || dirtyPercent > 100 {
		return largeRestoreBenchmarkPlan{}, fmt.Errorf("dirty percent must be in [1, 100]")
	}
	if pageBytes <= 0 {
		return largeRestoreBenchmarkPlan{}, fmt.Errorf("page bytes must be positive")
	}

	extentBytes := int64(sizeMiB) * 1024 * 1024
	totalPages := int(extentBytes / int64(pageBytes))
	if totalPages == 0 || extentBytes%int64(pageBytes) != 0 {
		return largeRestoreBenchmarkPlan{}, fmt.Errorf("extent must contain a whole number of tracking pages")
	}

	return largeRestoreBenchmarkPlan{
		extentBytes:         extentBytes,
		pageBytes:           pageBytes,
		totalPages:          totalPages,
		requestedDirtyPages: totalPages * dirtyPercent / 100,
	}, nil
}

func reportLargeRestoreMetrics(reporter benchmarkMetricReporter, plan largeRestoreBenchmarkPlan, observedDirtyPages *int, fallback bool) {
	reporter.ReportMetric(float64(plan.requestedDirtyPages), "requested_pages/op")
	if observedDirtyPages != nil {
		reporter.ReportMetric(float64(*observedDirtyPages), "observed_pages/op")
	}
	reporter.ReportMetric(float64(plan.requestedDirtyPages*plan.pageBytes), "dirty_B/op")
	reporter.ReportMetric(float64(plan.extentBytes), "extent_B/op")
	if fallback {
		reporter.ReportMetric(1, "fallback")
	} else {
		reporter.ReportMetric(0, "fallback")
	}
}

//go:build !linux

package main

import (
	"runtime"
)

func collectProcessMetrics() ProcessMetrics {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return ProcessMetrics{
		Platform:    runtime.GOOS,
		Goroutines:  runtime.NumGoroutine(),
		GoHeapAlloc: mem.HeapAlloc,
		GoSys:       mem.Sys,
	}
}

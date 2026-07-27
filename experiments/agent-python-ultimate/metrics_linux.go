//go:build linux

package main

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

func collectProcessMetrics() ProcessMetrics {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	metrics := ProcessMetrics{Platform: "linux", Goroutines: runtime.NumGoroutine(), GoHeapAlloc: mem.HeapAlloc, GoSys: mem.Sys}
	applyKVKiB("/proc/self/status", map[string]*uint64{
		"VmRSS": &metrics.RSSBytes, "VmHWM": &metrics.PeakRSSBytes, "VmSize": &metrics.VirtualBytes,
	})
	applyKVKiB("/proc/self/smaps_rollup", map[string]*uint64{
		"Pss": &metrics.PSSBytes, "Private_Dirty": &metrics.PrivateDirtyBytes,
	})
	if raw, err := os.ReadFile("/proc/self/stat"); err == nil {
		text := string(raw)
		if closeParen := strings.LastIndex(text, ")"); closeParen >= 0 {
			fields := strings.Fields(text[closeParen+1:])
			metrics.MinorFaults = parseUintField(fields, 7)
			metrics.MajorFaults = parseUintField(fields, 9)
			metrics.UserTicks = parseUintField(fields, 11)
			metrics.SystemTicks = parseUintField(fields, 12)
		}
	}
	if raw, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "0::") {
				path := "/sys/fs/cgroup" + strings.TrimPrefix(line, "0::")
				metrics.CgroupMemory = readUintFile(path + "/memory.current")
				metrics.CgroupMemoryPeak = readUintFile(path + "/memory.peak")
				break
			}
		}
	}
	return metrics
}

func applyKVKiB(path string, targets map[string]*uint64) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSuffix(parts[0], ":")
		if target := targets[key]; target != nil {
			value, _ := strconv.ParseUint(parts[1], 10, 64)
			*target = value * 1024
		}
	}
}

func parseUintField(fields []string, index int) uint64 {
	if index < 0 || index >= len(fields) {
		return 0
	}
	value, _ := strconv.ParseUint(fields[index], 10, 64)
	return value
}

func readUintFile(path string) uint64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	value, _ := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	return value
}

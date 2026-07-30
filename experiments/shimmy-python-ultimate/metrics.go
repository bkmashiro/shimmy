package main

// ProcessMetrics is a point-in-time process/cgroup resource sample.
type ProcessMetrics struct {
	Platform          string `json:"platform"`
	RSSBytes          uint64 `json:"rss_bytes,omitempty"`
	PeakRSSBytes      uint64 `json:"peak_rss_bytes,omitempty"`
	VirtualBytes      uint64 `json:"virtual_bytes,omitempty"`
	PSSBytes          uint64 `json:"pss_bytes,omitempty"`
	PrivateDirtyBytes uint64 `json:"private_dirty_bytes,omitempty"`
	MinorFaults       uint64 `json:"minor_faults,omitempty"`
	MajorFaults       uint64 `json:"major_faults,omitempty"`
	UserTicks         uint64 `json:"user_ticks,omitempty"`
	SystemTicks       uint64 `json:"system_ticks,omitempty"`
	CgroupMemory      uint64 `json:"cgroup_memory_current,omitempty"`
	CgroupMemoryPeak  uint64 `json:"cgroup_memory_peak,omitempty"`
	Goroutines        int    `json:"goroutines"`
	GoHeapAlloc       uint64 `json:"go_heap_alloc"`
	GoSys             uint64 `json:"go_sys"`
}

// Lambda kernel-feature probe.
//
// Build (static, linux/amd64 for Lambda):
//
//	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
//	  go build -ldflags="-s -w" -o lambda-probe ./tools/lambda-probe
//
// Deploy to Lambda as a Go bootstrap binary or wrap in a thin Python/Node
// handler that execs it and returns stdout.
//
// Output: one JSON object per line, final line is "summary" with overall pass/fail.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// ---- result helpers --------------------------------------------------------

type result struct {
	Probe   string `json:"probe"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail,omitempty"`
	WarnMsg string `json:"warn,omitempty"`
}

var results []result

func emit(r result) {
	b, _ := json.Marshal(r)
	fmt.Println(string(b))
	results = append(results, r)
}

func pass(probe, detail string) {
	emit(result{Probe: probe, OK: true, Detail: detail})
}

func fail(probe, detail string) {
	emit(result{Probe: probe, OK: false, Detail: detail})
}

func warn(probe, detail, w string) {
	emit(result{Probe: probe, OK: true, Detail: detail, WarnMsg: w})
}

// ---- probes ----------------------------------------------------------------

// 1. Basic /proc/self/status readable
func probeProcStatus() {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		fail("proc_status_read", err.Error())
		return
	}
	// Extract Name line
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "Name:") {
			pass("proc_status_read", strings.TrimSpace(line))
			return
		}
	}
	pass("proc_status_read", "ok (no Name line found)")
}

// 2. /proc/self/pagemap readable (needed for soft-dirty page detection)
func probeProcPagemap() {
	f, err := os.Open("/proc/self/pagemap")
	if err != nil {
		fail("proc_pagemap_open", err.Error())
		return
	}
	f.Close()
	pass("proc_pagemap_open", "readable")
}

// 3. /proc/self/clear_refs writable with value "4" (soft-dirty reset)
//
// Writing "4\n" to clear_refs resets the soft-dirty bit on all PTEs for this
// process. This is the epoch-reset step for soft-dirty dirty-page tracking.
// Requires kernel >= 3.18 and CONFIG_MEM_SOFT_DIRTY=y.
func probeClearRefs() {
	err := os.WriteFile("/proc/self/clear_refs", []byte("4\n"), 0)
	if err != nil {
		fail("proc_clear_refs_write4", err.Error())
		return
	}
	pass("proc_clear_refs_write4", "soft-dirty reset: ok")
}

// 4. Read /proc/self/pagemap entry and check soft-dirty bit (PFN bit 55)
//
// We allocate a page, write to it (should set soft-dirty), reset via
// clear_refs=4, write again, then check bit 55 in the pagemap entry.
func probeSoftDirtyRoundTrip() {
	const pageSize = 4096

	// Allocate a page-aligned buffer via mmap
	b, err := syscall.Mmap(-1, 0, pageSize, syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS)
	if err != nil {
		fail("soft_dirty_roundtrip", "mmap failed: "+err.Error())
		return
	}
	defer syscall.Munmap(b)

	// Reset soft-dirty bits
	if err := os.WriteFile("/proc/self/clear_refs", []byte("4\n"), 0); err != nil {
		fail("soft_dirty_roundtrip", "clear_refs reset failed: "+err.Error())
		return
	}

	// Write to the page — should set soft-dirty
	b[0] = 42

	// Read pagemap entry for this page
	addr := uintptr(unsafe.Pointer(&b[0]))
	pageIndex := addr / pageSize

	f, err := os.Open("/proc/self/pagemap")
	if err != nil {
		fail("soft_dirty_roundtrip", "pagemap open: "+err.Error())
		return
	}
	defer f.Close()

	var entry [8]byte
	offset := int64(pageIndex * 8)
	if _, err := f.ReadAt(entry[:], offset); err != nil {
		fail("soft_dirty_roundtrip", "pagemap read: "+err.Error())
		return
	}

	val := uint64(entry[0]) | uint64(entry[1])<<8 | uint64(entry[2])<<16 |
		uint64(entry[3])<<24 | uint64(entry[4])<<32 | uint64(entry[5])<<40 |
		uint64(entry[6])<<48 | uint64(entry[7])<<56

	// Bit 55 = soft-dirty
	softDirty := (val>>55)&1 == 1
	detail := fmt.Sprintf("pagemap entry=0x%016x soft_dirty_bit=%v", val, softDirty)

	if softDirty {
		pass("soft_dirty_roundtrip", detail)
	} else {
		fail("soft_dirty_roundtrip", detail+" (bit 55 not set after write — kernel may lack CONFIG_MEM_SOFT_DIRTY)")
	}
}

// 5. userfaultfd fd creation (unprivileged)
func probeUserfaultfdFd() {
	// syscall number for userfaultfd on amd64 = 323, arm64 = 282
	var nr uintptr
	switch runtime.GOARCH {
	case "amd64":
		nr = 323
	case "arm64":
		nr = 282
	default:
		warn("userfaultfd_fd", "unknown arch "+runtime.GOARCH, "skipped")
		return
	}

	fd, _, errno := syscall.Syscall(nr, syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0, 0)
	if errno != 0 {
		fail("userfaultfd_fd", "syscall failed: "+errno.Error())
		return
	}
	syscall.Close(int(fd))
	pass("userfaultfd_fd", "fd created and closed")
}

// 6. /proc/self/smaps_rollup readable (memory stats, nice-to-have)
func probeSmaps() {
	b, err := os.ReadFile("/proc/self/smaps_rollup")
	if err != nil {
		warn("proc_smaps_rollup", "not readable: "+err.Error(), "non-critical")
		return
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	pass("proc_smaps_rollup", fmt.Sprintf("%d lines", len(lines)))
}

// 7a. Read vm.mmap_min_addr sysctl directly
func probeMmapMinAddr() {
	b, err := os.ReadFile("/proc/sys/vm/mmap_min_addr")
	if err != nil {
		fail("mmap_min_addr_sysctl", "not readable: "+err.Error())
		return
	}
	val := strings.TrimSpace(string(b))
	pass("mmap_min_addr_sysctl", "vm.mmap_min_addr="+val)
}

// 7b. mmap(addr=0, PROT_RW) — check whether returned address is actually 0
// or rounded up to mmap_min_addr (typically 4096 on hardened kernels).
func probeMmapZero() {
	b, err := syscall.Mmap(-1, 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS)
	if err != nil {
		fail("mmap_addr0_rw", "mmap failed: "+err.Error())
		return
	}
	actualAddr := uintptr(unsafe.Pointer(&b[0]))
	syscall.Munmap(b)
	detail := fmt.Sprintf("returned addr=0x%x (%d)", actualAddr, actualAddr)
	if actualAddr == 0 {
		pass("mmap_addr0_rw", detail+" — true null-page mapping")
	} else {
		warn("mmap_addr0_rw", detail+" — addr rounded up (mmap_min_addr>0)", "zpoline requires addr=0")
	}
}

// 7c. mmap(PROT_EXEC) — blocked by Lambda seccomp?
func probeMmapExec() {
	b, err := syscall.Mmap(-1, 0, 4096, syscall.PROT_READ|syscall.PROT_EXEC,
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS)
	if err != nil {
		fail("mmap_prot_exec", err.Error()+" — DynamoRIO/zpoline not viable")
		return
	}
	syscall.Munmap(b)
	pass("mmap_prot_exec", "succeeded")
}

// 8. /proc/sys/vm/unprivileged_userfaultfd
func probeUnprivilegedUffdSysctl() {
	b, err := os.ReadFile("/proc/sys/vm/unprivileged_userfaultfd")
	if err != nil {
		fail("sysctl_unprivileged_uffd", "not readable: "+err.Error())
		return
	}
	val := strings.TrimSpace(string(b))
	n, _ := strconv.Atoi(val)
	if n == 1 {
		pass("sysctl_unprivileged_uffd", "value=1 (unprivileged uffd enabled)")
	} else {
		fail("sysctl_unprivileged_uffd", "value="+val+" (0 means uffd requires CAP_SYS_PTRACE)")
	}
}

// 9. /proc/self/mem writable (alternative dirty-detection mechanism)
func probeProcMem() {
	f, err := os.OpenFile("/proc/self/mem", os.O_RDWR, 0)
	if err != nil {
		fail("proc_mem_rdwr", err.Error())
		return
	}
	f.Close()
	pass("proc_mem_rdwr", "writable")
}

// 10. Kernel version
func probeKernelVersion() {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		fail("kernel_version", err.Error())
		return
	}
	// Convert int8 array to string
	b := make([]byte, 0, len(u.Release))
	for _, c := range u.Release {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	pass("kernel_version", string(b))
}

// ---- summary ---------------------------------------------------------------

func main() {
	fmt.Println("# shimmy Lambda kernel-feature probe")
	fmt.Println("# arch:", runtime.GOARCH, "| os:", runtime.GOOS)
	fmt.Println()

	probeProcStatus()
	probeKernelVersion()
	probeProcPagemap()
	probeClearRefs()
	probeSoftDirtyRoundTrip()
	probeUnprivilegedUffdSysctl()
	probeUserfaultfdFd()
	probeSmaps()
	probeMmapMinAddr()
	probeMmapZero()
	probeMmapExec()
	probeProcMem()

	// Summary
	var passed, failed int
	for _, r := range results {
		if r.OK {
			passed++
		} else {
			failed++
		}
	}

	type summary struct {
		Probe  string `json:"probe"`
		Passed int    `json:"passed"`
		Failed int    `json:"failed"`
		OK     bool   `json:"ok"`
	}
	b, _ := json.Marshal(summary{
		Probe:  "summary",
		Passed: passed,
		Failed: failed,
		OK:     failed == 0,
	})
	fmt.Println()
	fmt.Println(string(b))

	if failed > 0 {
		os.Exit(1)
	}
}

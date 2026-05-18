//go:build linux && cgo

package wasm

// MprotectStrategy tracks dirty pages by write-protecting the WASM linear
// memory region with mprotect(PROT_READ) and catching SIGSEGV faults via a C
// signal handler installed with SA_SIGACTION.
//
// On each write fault:
//  1. The C handler invokes the goDirtyPageCallback Go export.
//  2. goDirtyPageCallback marks the page dirty and calls mprotect to restore
//     PROT_READ|PROT_WRITE so the faulting thread can continue.
//
// On Restore, only dirty pages are copied back from the snapshot and the entire
// region is re-protected for the next request.
//
// Cost: Take = O(N) copy + mprotect(whole region)
//
//	Restore = O(D) page copies + mprotect(whole region)  (D = dirty pages)
//	Per write fault = O(1) mprotect(one page)
//
// Caveats:
//   - Requires CGO (C signal handler).
//   - Installs a global SIGSEGV handler; only one MprotectStrategy may be
//     active at a time. A global mutex guards registration.
//   - Chains to Go's runtime SIGSEGV handler for faults outside the registered
//     region; the runtime handler deals with nil-pointer panics etc.
//   - Not suitable for environments where installing SA_SIGACTION is
//     restricted (e.g. strict seccomp profiles).

/*
#cgo CFLAGS: -O2
#include <signal.h>
#include <sys/mman.h>
#include <stdint.h>
#include <string.h>
#include <unistd.h>

// Forward declaration of the Go callback (exported via //export).
extern void goDirtyPageCallback(uint64_t addr);

static struct sigaction g_prev_sigsegv;
static volatile int     g_active   = 0;
static uint64_t         g_base     = 0;
static uint64_t         g_size     = 0;

static void mprotect_sigsegv(int sig, siginfo_t *info, void *ctx) {
    uint64_t addr = (uint64_t)(uintptr_t)info->si_addr;
    if (g_active && addr >= g_base && addr < g_base + g_size) {
        goDirtyPageCallback(addr);
        return;
    }
    // Chain to the previous handler (Go runtime or SIG_DFL).
    if (g_prev_sigsegv.sa_flags & SA_SIGACTION) {
        g_prev_sigsegv.sa_sigaction(sig, info, ctx);
    } else if (g_prev_sigsegv.sa_handler != SIG_DFL &&
               g_prev_sigsegv.sa_handler != SIG_IGN) {
        g_prev_sigsegv.sa_handler(sig);
    }
    // For SIG_DFL we do nothing — Go's runtime will have already set up an
    // alternate stack and the default action (core dump) will proceed.
}

static int mprotect_install(uint64_t base, uint64_t size) {
    g_base   = base;
    g_size   = size;
    g_active = 0;
    struct sigaction sa;
    memset(&sa, 0, sizeof(sa));
    sa.sa_sigaction = mprotect_sigsegv;
    sigemptyset(&sa.sa_mask);
    sa.sa_flags = SA_SIGACTION | SA_ONSTACK | SA_NODEFER;
    return sigaction(SIGSEGV, &sa, &g_prev_sigsegv);
}

static void mprotect_remove(void) {
    g_active = 0;
    sigaction(SIGSEGV, &g_prev_sigsegv, NULL);
}

static void mprotect_set_active(int v) { g_active = v; }

static int mprotect_ro(uint64_t addr, uint64_t len) {
    return mprotect((void *)(uintptr_t)addr, (size_t)len, PROT_READ);
}

static int mprotect_rw_page(uint64_t addr, int pgsz) {
    uintptr_t page = (uintptr_t)addr & ~((uintptr_t)pgsz - 1);
    return mprotect((void *)page, (size_t)pgsz, PROT_READ | PROT_WRITE);
}

static int mprotect_rw(uint64_t addr, uint64_t len) {
    return mprotect((void *)(uintptr_t)addr, (size_t)len, PROT_READ | PROT_WRITE);
}
*/
import "C"

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/tetratelabs/wazero/api"
)

// mprotectGlobal guards the single active MprotectStrategy instance.
// Only one strategy may be active at a time because there is one global
// SIGSEGV handler.
var (
	mprotectGlobal   sync.Mutex
	mprotectActiveSt *MprotectStrategy
)

// goDirtyPageCallback is called from C when a write-protect SIGSEGV fires
// inside the WASM linear memory region. It marks the faulting page dirty and
// immediately re-allows writes so the faulting instruction can retry.
//
//export goDirtyPageCallback
func goDirtyPageCallback(addr C.uint64_t) {
	mprotectGlobal.Lock()
	s := mprotectActiveSt
	mprotectGlobal.Unlock()
	if s == nil {
		return
	}
	s.handleFault(uint64(addr))
}

// MprotectStrategy implements SnapshotStrategy via mprotect write-protection
// and SIGSEGV-based dirty page tracking.
type MprotectStrategy struct {
	basePtr   unsafe.Pointer
	memSize   uint32
	pageSize  int
	pageCount int
	snapshot  []byte

	mu    sync.Mutex
	dirty []bool
}

// NewMprotectStrategy creates a MprotectStrategy for the given WASM module
// memory. It installs the global SIGSEGV handler.
func NewMprotectStrategy(mem api.Memory) (*MprotectStrategy, error) {
	if mem == nil {
		return nil, fmt.Errorf("mprotect: nil api.Memory")
	}
	size := mem.Size()
	if size == 0 {
		return nil, fmt.Errorf("mprotect: zero-size linear memory")
	}

	buf, ok := mem.Read(0, size)
	if !ok {
		return nil, fmt.Errorf("mprotect: could not read linear memory (size=%d)", size)
	}
	basePtr := unsafe.Pointer(unsafe.SliceData(buf))

	pageSize := syscall.Getpagesize()
	pageCount := int((uint64(size) + uint64(pageSize) - 1) / uint64(pageSize))

	s := &MprotectStrategy{
		basePtr:   basePtr,
		memSize:   size,
		pageSize:  pageSize,
		pageCount: pageCount,
		snapshot:  make([]byte, size),
		dirty:     make([]bool, pageCount),
	}

	// Install the global C signal handler.
	if ret := C.mprotect_install(
		C.uint64_t(uintptr(basePtr)),
		C.uint64_t(size),
	); ret != 0 {
		return nil, fmt.Errorf("mprotect: sigaction install failed (ret=%d)", int(ret))
	}

	return s, nil
}

// handleFault is called by goDirtyPageCallback when a fault fires inside our
// region. It marks the page dirty and lifts write-protection on that page.
func (s *MprotectStrategy) handleFault(addr uint64) {
	base := uint64(uintptr(s.basePtr))
	if addr < base || addr >= base+uint64(s.memSize) {
		return
	}
	pageIdx := int((addr - base) / uint64(s.pageSize))

	// Lift WP on this page BEFORE marking dirty so the faulting thread can
	// immediately retry. mprotect_rw_page aligns addr to page boundary.
	C.mprotect_rw_page(C.uint64_t(addr), C.int(s.pageSize))

	s.mu.Lock()
	if pageIdx < len(s.dirty) {
		s.dirty[pageIdx] = true
	}
	s.mu.Unlock()
}

// Take implements SnapshotStrategy. It copies the current linear memory into
// the snapshot and write-protects the entire region.
func (s *MprotectStrategy) Take(mem api.Memory) error {
	if mem == nil {
		return nil
	}

	// Ensure we're the active strategy (only one at a time).
	mprotectGlobal.Lock()
	if mprotectActiveSt != nil && mprotectActiveSt != s {
		mprotectGlobal.Unlock()
		return fmt.Errorf("mprotect: another MprotectStrategy is already active")
	}
	mprotectActiveSt = s
	mprotectGlobal.Unlock()

	// Snapshot current memory contents.
	raw := unsafe.Slice((*byte)(s.basePtr), s.memSize)
	s.mu.Lock()
	copy(s.snapshot, raw)
	for i := range s.dirty {
		s.dirty[i] = false
	}
	s.mu.Unlock()

	// Write-protect the entire region.
	if ret := C.mprotect_ro(
		C.uint64_t(uintptr(s.basePtr)),
		C.uint64_t(s.memSize),
	); ret != 0 {
		return fmt.Errorf("mprotect: mprotect(PROT_READ) failed (ret=%d)", int(ret))
	}

	// Activate fault tracking.
	C.mprotect_set_active(1)
	return nil
}

// Restore implements SnapshotStrategy. It restores dirty pages from the
// snapshot and re-protects the region for the next request.
func (s *MprotectStrategy) Restore(mem api.Memory) error {
	if mem == nil || s.snapshot == nil {
		return nil
	}

	// Pause fault tracking while we restore (our own writes shouldn't be
	// counted as guest writes).
	C.mprotect_set_active(0)

	s.mu.Lock()
	var dirtyPages []int
	for i, d := range s.dirty {
		if d {
			dirtyPages = append(dirtyPages, i)
			s.dirty[i] = false
		}
	}
	s.mu.Unlock()

	pageSize := s.pageSize
	for _, pg := range dirtyPages {
		off := uint32(pg * pageSize)
		end := off + uint32(pageSize)
		if end > s.memSize {
			end = s.memSize
		}
		// Dirty pages are already PROT_READ|PROT_WRITE (lifted in handleFault).
		if !mem.Write(off, s.snapshot[off:end]) {
			return fmt.Errorf("mprotect: Restore: mem.Write failed at offset %d", off)
		}
	}

	// Re-protect the entire region for the next request's fault tracking.
	if ret := C.mprotect_ro(
		C.uint64_t(uintptr(s.basePtr)),
		C.uint64_t(s.memSize),
	); ret != 0 {
		return fmt.Errorf("mprotect: Restore: re-protect failed (ret=%d)", int(ret))
	}

	// Resume fault tracking.
	C.mprotect_set_active(1)
	return nil
}

// DirtyPageCount returns the number of pages dirtied since the last Take or
// Restore. For benchmarking.
func (s *MprotectStrategy) DirtyPageCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, d := range s.dirty {
		if d {
			n++
		}
	}
	return n
}

// Close implements SnapshotStrategy. It deactivates fault tracking, removes
// the SIGSEGV handler, and restores write access to the memory region.
func (s *MprotectStrategy) Close() error {
	C.mprotect_set_active(0)

	mprotectGlobal.Lock()
	if mprotectActiveSt == s {
		mprotectActiveSt = nil
	}
	mprotectGlobal.Unlock()

	C.mprotect_remove()

	// Restore full read-write access so the caller can use the memory again.
	C.mprotect_rw(
		C.uint64_t(uintptr(s.basePtr)),
		C.uint64_t(s.memSize),
	)

	s.snapshot = nil
	return nil
}

//go:build linux && cgo

package wasm

// MprotectStrategy tracks dirty pages by write-protecting the WASM linear
// memory region with mprotect(PROT_READ) and catching SIGSEGV faults via a C
// signal handler installed with SA_SIGINFO.
//
// Signal-safety: goDirtyPageCallback is called from a C signal handler context.
// It must not enter the Go scheduler, allocate, or take any Go mutex. All dirty-
// page state is recorded via C-level atomic stores into a bitmap allocated with
// malloc (so the GC never touches it). The Go runtime is only entered after the
// signal handler returns, in Take/Restore which run on normal goroutine stacks.

/*
#cgo CFLAGS: -O2
#include <signal.h>
#include <sys/mman.h>
#include <stdint.h>
#include <string.h>
#include <stdlib.h>
#include <unistd.h>
#include <stdatomic.h>

static struct sigaction g_prev_sigsegv;
static volatile int     g_active    = 0;
static uint64_t         g_base      = 0;
static uint64_t         g_size      = 0;
static int              g_page_size = 4096;

// Dirty bitmap: one bit per page, stored in a malloc'd array of uint64_t words.
// Accessed only with atomic operations so no mutex is needed in the signal handler.
static _Atomic uint64_t *g_dirty_words = NULL;
static int               g_dirty_nwords = 0;

// mark_dirty_page: atomically set the bit for the given page index.
// Signal-safe: uses only atomic RMW, no locks, no syscalls besides mprotect.
static void mark_dirty_page(int page_idx) {
    int word = page_idx / 64;
    int bit  = page_idx % 64;
    if (word < g_dirty_nwords) {
        atomic_fetch_or_explicit(&g_dirty_words[word],
                                 (uint64_t)1 << bit,
                                 memory_order_relaxed);
    }
}

static void mprotect_sigsegv(int sig, siginfo_t *info, void *ctx) {
    uint64_t addr = (uint64_t)(uintptr_t)info->si_addr;
    if (g_active && addr >= g_base && addr < g_base + g_size) {
        // Compute page index and mark dirty (signal-safe atomic).
        int page_idx = (int)((addr - g_base) / (uint64_t)g_page_size);
        mark_dirty_page(page_idx);

        // Lift write-protection on this page so the faulting store can retry.
        uintptr_t page_addr = (uintptr_t)addr & ~((uintptr_t)g_page_size - 1);
        mprotect((void *)page_addr, (size_t)g_page_size, PROT_READ | PROT_WRITE);
        return;
    }
    // Chain to the previous handler (Go runtime handles nil-pointer panics etc.).
    if (g_prev_sigsegv.sa_flags & SA_SIGINFO) {
        g_prev_sigsegv.sa_sigaction(sig, info, ctx);
    } else if (g_prev_sigsegv.sa_handler != SIG_DFL &&
               g_prev_sigsegv.sa_handler != SIG_IGN) {
        g_prev_sigsegv.sa_handler(sig);
    }
}

static int mprotect_install(uint64_t base, uint64_t size, int page_size, int page_count) {
    g_base      = base;
    g_size      = size;
    g_page_size = page_size;
    g_active    = 0;

    int nwords = (page_count + 63) / 64;
    g_dirty_words  = (_Atomic uint64_t *)calloc((size_t)nwords, sizeof(_Atomic uint64_t));
    g_dirty_nwords = nwords;
    if (!g_dirty_words) return -1;

    struct sigaction sa;
    memset(&sa, 0, sizeof(sa));
    sa.sa_sigaction = mprotect_sigsegv;
    sigemptyset(&sa.sa_mask);
    sa.sa_flags = SA_SIGINFO | SA_ONSTACK | SA_NODEFER;
    int ret = sigaction(SIGSEGV, &sa, &g_prev_sigsegv);
    if (ret != 0) {
        free(g_dirty_words);
        g_dirty_words = NULL;
    }
    return ret;
}

static void mprotect_remove(void) {
    g_active = 0;
    sigaction(SIGSEGV, &g_prev_sigsegv, NULL);
    free(g_dirty_words);
    g_dirty_words  = NULL;
    g_dirty_nwords = 0;
}

static void mprotect_set_active(int v)  { g_active = v; }

static int mprotect_ro(uint64_t addr, uint64_t len) {
    return mprotect((void *)(uintptr_t)addr, (size_t)len, PROT_READ);
}

static int mprotect_rw(uint64_t addr, uint64_t len) {
    return mprotect((void *)(uintptr_t)addr, (size_t)len, PROT_READ | PROT_WRITE);
}

// clear_dirty_bitmap resets all dirty bits atomically.
static void clear_dirty_bitmap(void) {
    for (int i = 0; i < g_dirty_nwords; i++) {
        atomic_store_explicit(&g_dirty_words[i], 0, memory_order_relaxed);
    }
}

// read_dirty_word returns the i-th 64-bit word of the dirty bitmap.
static uint64_t read_dirty_word(int i) {
    if (i < 0 || i >= g_dirty_nwords) return 0;
    return atomic_load_explicit(&g_dirty_words[i], memory_order_relaxed);
}

static int dirty_nwords(void) { return g_dirty_nwords; }
*/
import "C"

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/tetratelabs/wazero/api"
)

// mprotectMu guards NewMprotectStrategy / Close so only one instance is active.
var mprotectMu sync.Mutex

// MprotectStrategy implements SnapshotStrategy via mprotect write-protection
// and SIGSEGV-based dirty page tracking.
//
// The dirty-page bitmap lives in C-allocated memory and is updated exclusively
// via C atomic operations inside the signal handler — no Go runtime involvement
// during fault handling, so "morestack on g0" cannot occur.
type MprotectStrategy struct {
	basePtr   unsafe.Pointer
	memSize   uint32
	pageSize  int
	pageCount int
	snapshot  []byte
}

// NewMprotectStrategy creates a MprotectStrategy for the given WASM module
// memory and installs the global SIGSEGV handler.
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

	mprotectMu.Lock()
	defer mprotectMu.Unlock()

	ret := C.mprotect_install(
		C.uint64_t(uintptr(basePtr)),
		C.uint64_t(size),
		C.int(pageSize),
		C.int(pageCount),
	)
	if ret != 0 {
		return nil, fmt.Errorf("mprotect: sigaction install failed (ret=%d)", int(ret))
	}

	return &MprotectStrategy{
		basePtr:   basePtr,
		memSize:   size,
		pageSize:  pageSize,
		pageCount: pageCount,
		snapshot:  make([]byte, size),
	}, nil
}

// Take implements SnapshotStrategy. Copies current linear memory into the
// snapshot, write-protects the whole region, and clears the dirty bitmap.
func (s *MprotectStrategy) Take(mem api.Memory) error {
	if mem == nil {
		return nil
	}

	// Snapshot from the raw backing slice (no wazero copy).
	raw := unsafe.Slice((*byte)(s.basePtr), s.memSize)
	copy(s.snapshot, raw)

	// Write-protect the entire region.
	if ret := C.mprotect_ro(C.uint64_t(uintptr(s.basePtr)), C.uint64_t(s.memSize)); ret != 0 {
		return fmt.Errorf("mprotect: Take: mprotect(PROT_READ) failed")
	}

	// Clear dirty bitmap and activate fault tracking.
	C.clear_dirty_bitmap()
	C.mprotect_set_active(1)
	return nil
}

// Restore implements SnapshotStrategy. Restores only dirty pages, re-protects
// the region, and clears the dirty bitmap for the next request.
func (s *MprotectStrategy) Restore(mem api.Memory) error {
	if mem == nil || s.snapshot == nil {
		return nil
	}

	// Pause fault tracking while we restore.
	C.mprotect_set_active(0)

	// Collect dirty pages from C bitmap.
	nwords := int(C.dirty_nwords())
	pageSize := s.pageSize

	for w := 0; w < nwords; w++ {
		word := uint64(C.read_dirty_word(C.int(w)))
		if word == 0 {
			continue
		}
		for b := 0; b < 64; b++ {
			if word&(1<<uint(b)) == 0 {
				continue
			}
			pg := w*64 + b
			if pg >= s.pageCount {
				break
			}
			off := uint32(pg * pageSize)
			end := off + uint32(pageSize)
			if end > s.memSize {
				end = s.memSize
			}
			// Dirty pages are already PROT_READ|PROT_WRITE (lifted in handler).
			if !mem.Write(off, s.snapshot[off:end]) {
				return fmt.Errorf("mprotect: Restore: mem.Write failed at offset %d", off)
			}
		}
	}

	// Re-protect entire region and reset bitmap.
	if ret := C.mprotect_ro(C.uint64_t(uintptr(s.basePtr)), C.uint64_t(s.memSize)); ret != 0 {
		return fmt.Errorf("mprotect: Restore: re-protect failed")
	}
	C.clear_dirty_bitmap()
	C.mprotect_set_active(1)
	return nil
}

// DirtyPageCount returns the number of dirty pages since the last Take/Restore.
func (s *MprotectStrategy) DirtyPageCount() int {
	nwords := int(C.dirty_nwords())
	count := 0
	for w := 0; w < nwords; w++ {
		word := uint64(C.read_dirty_word(C.int(w)))
		count += int(popcount64(word))
	}
	return count
}

func popcount64(x uint64) int {
	n := 0
	for x != 0 {
		n += int(x & 1)
		x >>= 1
	}
	return n
}

// Close deactivates fault tracking, removes the SIGSEGV handler, and restores
// full read-write access.
func (s *MprotectStrategy) Close() error {
	C.mprotect_set_active(0)
	C.mprotect_remove()
	C.mprotect_rw(C.uint64_t(uintptr(s.basePtr)), C.uint64_t(s.memSize))
	return nil
}

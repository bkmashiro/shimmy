//go:build linux

package wasm

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/tetratelabs/wazero/api"
)

// SoftDirtyStrategy tracks dirty pages using the Linux soft-dirty PTE mechanism.
//
// After taking a snapshot it writes "4" to /proc/self/clear_refs to reset the
// soft-dirty bits on all PTEs in the process. When the WASM guest writes to a
// page the kernel automatically sets the soft-dirty bit in the page-table entry.
// On Restore, /proc/self/pagemap is read to find which pages have their soft-
// dirty bit set; only those pages are copied back from the snapshot.
//
// Cost: Take = O(N) copy + O(1) clear_refs write
//
//	Restore = O(N/pagesize) pagemap reads + O(D) page copies  (D = dirty pages)
//
// Availability: any Linux kernel >= 3.18 with CONFIG_CHECKPOINT_RESTORE=y
// (present in most distros). Does NOT require special privileges or seccomp
// exceptions, unlike uffd.
//
// Reference: https://www.kernel.org/doc/Documentation/vm/soft-dirty.txt
type SoftDirtyStrategy struct {
	basePtr   unsafe.Pointer
	memSize   uint32
	pageSize  int
	pageCount int
	snapshot  []byte
	// pinner keeps the WASM linear memory backing array pinned so the GC
	// cannot move it while pagemap offset calculations reference the address.
	// (C-1 fix)
	pinner runtime.Pinner
}

// NewSoftDirtyStrategy creates a SoftDirtyStrategy for the given WASM module
// memory. It validates that /proc/self/pagemap and /proc/self/clear_refs are
// accessible. Call Take once after module initialisation.
//
// SoftDirtyStrategy relies on process-wide soft-dirty PTE bits. With multiple
// WASM instances sharing the same process the bits from different instances
// cannot be distinguished, so the strategy is only safe when MaxInstances == 1.
// Pass maxInstances to enforce this guard; pass 1 if you have verified that
// only a single instance exists.
func NewSoftDirtyStrategy(mem api.Memory) (*SoftDirtyStrategy, error) {
	if mem == nil {
		return nil, fmt.Errorf("soft-dirty: nil api.Memory")
	}
	size := mem.Size()
	if size == 0 {
		return nil, fmt.Errorf("soft-dirty: zero-size linear memory")
	}

	buf, ok := mem.Read(0, size)
	if !ok {
		return nil, fmt.Errorf("soft-dirty: could not read linear memory (size=%d)", size)
	}
	basePtr := unsafe.Pointer(unsafe.SliceData(buf))

	// Pin the backing array so the GC cannot move it while pagemap offset
	// calculations reference the address. (C-1 fix)
	var pinner runtime.Pinner
	pinner.Pin(unsafe.SliceData(buf))

	// Validate /proc/self/pagemap is readable.
	pmf, err := os.Open("/proc/self/pagemap")
	if err != nil {
		pinner.Unpin()
		return nil, fmt.Errorf("soft-dirty: open /proc/self/pagemap: %w", err)
	}
	pmf.Close()

	// Validate /proc/self/clear_refs is writable.
	if err := os.WriteFile("/proc/self/clear_refs", []byte("4"), 0); err != nil {
		pinner.Unpin()
		return nil, fmt.Errorf("soft-dirty: write /proc/self/clear_refs: %w", err)
	}

	pageSize := syscall.Getpagesize()
	pageCount := int((uint64(size) + uint64(pageSize) - 1) / uint64(pageSize))

	return &SoftDirtyStrategy{
		basePtr:   basePtr,
		memSize:   size,
		pageSize:  pageSize,
		pageCount: pageCount,
		snapshot:  make([]byte, size),
		pinner:    pinner,
	}, nil
}

// Take implements SnapshotStrategy. It copies the entire linear memory into the
// internal snapshot buffer and resets the soft-dirty bits so subsequent writes
// can be detected.
func (s *SoftDirtyStrategy) Take(mem api.Memory) error {
	if mem == nil {
		return nil
	}

	// Read directly from the backing memory slice (no wazero copy).
	raw := unsafe.Slice((*byte)(s.basePtr), s.memSize)
	copy(s.snapshot, raw)

	// Reset soft-dirty bits for all pages in the process.
	return clearSoftDirtyBits()
}

// Restore implements SnapshotStrategy. It reads /proc/self/pagemap to identify
// pages written since the last Take and copies back only those pages from the
// snapshot.
func (s *SoftDirtyStrategy) Restore(mem api.Memory) error {
	if mem == nil || s.snapshot == nil {
		return nil
	}

	dirtyPages, err := s.readDirtyPageIndices()
	if err != nil {
		return fmt.Errorf("soft-dirty: Restore: %w", err)
	}

	pageSize := s.pageSize
	for _, pg := range dirtyPages {
		off := uint32(pg * pageSize)
		end := off + uint32(pageSize)
		if end > s.memSize {
			end = s.memSize
		}
		if !mem.Write(off, s.snapshot[off:end]) {
			return fmt.Errorf("soft-dirty: Restore: mem.Write failed at offset %d", off)
		}
	}

	// Reset soft-dirty bits so the next request starts clean.
	return clearSoftDirtyBits()
}

// Close implements SnapshotStrategy. SoftDirtyStrategy holds no OS resources.
func (s *SoftDirtyStrategy) Close() error {
	s.snapshot = nil
	s.pinner.Unpin() // C-1 fix: release GC pin
	return nil
}

// DirtyPageCount returns the number of pages written since the last Take/Restore.
// It reads /proc/self/pagemap on every call; intended for benchmarking.
func (s *SoftDirtyStrategy) DirtyPageCount() (int, error) {
	pages, err := s.readDirtyPageIndices()
	return len(pages), err
}

// readDirtyPageIndices reads /proc/self/pagemap and returns the indices of pages
// (relative to the WASM linear memory base) that have their soft-dirty bit set.
//
// Implementation: one bulk ReadAt covers the entire WASM linear memory region
// (pageCount × 8 bytes), replacing the previous per-page ReadAt loop that
// issued pageCount separate syscalls (~3584 for a 14 MB region).
func (s *SoftDirtyStrategy) readDirtyPageIndices() ([]int, error) {
	f, err := os.Open("/proc/self/pagemap")
	if err != nil {
		return nil, fmt.Errorf("open /proc/self/pagemap: %w", err)
	}
	defer f.Close()

	// Page-map entries are 8 bytes each, indexed by virtual page number (VPN).
	// VPN = virtual_address / page_size.
	// Bit 55 = soft-dirty flag.
	const softDirtyBit = uint64(1) << 55

	baseVPN := uintptr(s.basePtr) / uintptr(s.pageSize)
	offset := int64(baseVPN) * 8

	// Single bulk read: all page entries for the WASM linear memory region.
	buf := make([]byte, s.pageCount*8)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return nil, fmt.Errorf("pagemap bulk ReadAt (offset=%d, len=%d): %w", offset, len(buf), err)
	}

	var dirty []int
	for pg := 0; pg < s.pageCount; pg++ {
		entry := binary.LittleEndian.Uint64(buf[pg*8 : pg*8+8])
		if entry&softDirtyBit != 0 {
			dirty = append(dirty, pg)
		}
	}
	return dirty, nil
}

// clearSoftDirtyBits writes "4" to /proc/self/clear_refs, which resets the
// soft-dirty bit on every PTE in the calling process without affecting
// page residency.
func clearSoftDirtyBits() error {
	if err := os.WriteFile("/proc/self/clear_refs", []byte("4"), 0); err != nil {
		return fmt.Errorf("soft-dirty: clear_refs: %w", err)
	}
	return nil
}

//go:build linux

package wasm

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/tetratelabs/wazero/api"
)

// ---------------------------------------------------------------------------
// UffdProbeStrategy — userfaultfd write-protect probe
// ---------------------------------------------------------------------------
//
// UffdProbeStrategy validates that the Linux userfaultfd(2) subsystem is
// available and that UFFDIO_REGISTER_MODE_WP (write-protect tracking) can be
// used in this environment. It holds an open uffd file descriptor and a small
// test mmap region registered in WP mode to prove the mechanism works end-to-
// end.
//
// For the actual Take / Restore operations it delegates to FullMemcpyStrategy.
// UffdProbeStrategy is only used as an availability probe and in the default
// NewSnapshotStrategy() factory. For real dirty-page tracking, the supervisor
// uses UffdStrategy directly via selectStrategy(mem).
//
// Note: the original design concern about "wazero not exposing raw memory
// pointer" has been resolved: wazero's api.Memory.Read(0, size) returns a
// direct slice into the backing buffer (not a copy), so unsafe.SliceData gives
// the base address. For a make([]byte, N) large allocation, Go uses
// mmap(MAP_ANON|MAP_PRIVATE), which is compatible with UFFDIO_REGISTER_MODE_WP.
// UffdStrategy exploits this to register wazero linear memory directly.
//
// Fallback: if New() returns an error the caller should use FullMemcpyStrategy.

// ---------------------------------------------------------------------------
// Syscall number
// ---------------------------------------------------------------------------

// uffdSyscallNr is the x86-64 Linux syscall number for userfaultfd(2).
// bench_test.go also declares sysUserfaultfd (= 323) for the same purpose;
// they are in the same package so we use the same constant name to avoid
// redeclaration, but the production code needs it at non-test build time.
const uffdSyscallNr = 323

// ---------------------------------------------------------------------------
// ioctl constants (linux/userfaultfd.h, x86-64)
// ---------------------------------------------------------------------------

const (
	uffdStrategyAPI         = 0xaa   // UFFD_API magic version
	uffdStrategyFeatureWP   = 1 << 2 // UFFD_FEATURE_PAGEFAULT_FLAG_WP
	uffdStrategyRegisterMWP = 1 << 1 // UFFDIO_REGISTER_MODE_WP

	ioctlUffdioAPIStrategy      = 0xc018aa3f // UFFDIO_API
	ioctlUffdioRegisterStrategy = 0xc020aa00 // UFFDIO_REGISTER
	ioctlUffdioWPStrategy       = 0xc018aa06 // UFFDIO_WRITEPROTECT — _IOWR(0xAA,6,struct uffdio_writeprotect{range[16]+mode[8]=24=0x18})

	uffdOCloexecStrategy  = 0x80000 // O_CLOEXEC
	uffdONonblockStrategy = 0x800   // O_NONBLOCK
)

// ---------------------------------------------------------------------------
// Kernel ABI structs
// ---------------------------------------------------------------------------

type uffdioAPIStructStrategy struct {
	api      uint64
	features uint64
	ioctls   uint64
}

type uffdioRangeStrategy struct {
	start uint64
	len   uint64
}

type uffdioRegisterStrategy struct {
	uffdioRangeStrategy
	mode   uint64
	ioctls uint64
}

type uffdioWPStrategy struct {
	uffdioRangeStrategy
	mode uint64
}

// UFFDIO_WRITEPROTECT mode flags.
const (
	uffdioWPModeWP   = 1 << 0 // UFFDIO_WRITEPROTECT_MODE_WP — arm write-protection
	uffdioWPModeDont = 1 << 1 // UFFDIO_WRITEPROTECT_MODE_DONTWAKE (unused here)
)

func ioctlUffd(fd uintptr, req uint, arg uintptr) syscall.Errno {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(req), arg)
	return errno
}

// ---------------------------------------------------------------------------
// UffdProbeStrategy
// ---------------------------------------------------------------------------

// UffdProbeStrategy holds a live uffd fd and a test mmap region registered
// with WP mode to confirm the mechanism is operational.
type UffdProbeStrategy struct {
	uffdFd     int
	testRegion uintptr
	testSize   uintptr

	// dirtyPages records which page indices (relative to testRegion) were
	// written since the last Take. Currently unused for the wasm memory path
	// (see package-level comment).
	dirtyPages map[int]struct{}

	// fallback handles the actual wasm memory snapshot/restore until the
	// wazero memory address is accessible.
	fallback *FullMemcpyStrategy
}

// NewUffdProbeStrategy creates a UffdProbeStrategy. It:
//  1. Opens a userfaultfd fd
//  2. Performs the UFFDIO_API handshake and checks for WP feature
//  3. mmap's a one-page test region
//  4. Registers the test region with UFFDIO_REGISTER_MODE_WP
//  5. Arms the region with UFFDIO_WRITEPROTECT (WP=true)
//
// If any step fails, an error is returned and the caller should fall back to
// FullMemcpyStrategy.
func NewUffdProbeStrategy() (*UffdProbeStrategy, error) {
	// Step 1: open uffd fd.
	fd, _, errno := syscall.RawSyscall(
		uffdSyscallNr,
		uffdOCloexecStrategy|uffdONonblockStrategy,
		0, 0,
	)
	if errno != 0 {
		return nil, fmt.Errorf("uffd: userfaultfd syscall: %w", errno)
	}

	ok := false
	defer func() {
		if !ok {
			syscall.Close(int(fd)) //nolint:errcheck
		}
	}()

	// Step 2: UFFDIO_API handshake.
	apiStruct := uffdioAPIStructStrategy{
		api:      uffdStrategyAPI,
		features: 0,
	}
	if err := ioctlUffd(fd, ioctlUffdioAPIStrategy, uintptr(unsafe.Pointer(&apiStruct))); err != 0 {
		return nil, fmt.Errorf("uffd: UFFDIO_API handshake: %w", err)
	}

	if apiStruct.features&uffdStrategyFeatureWP == 0 {
		return nil, fmt.Errorf("uffd: kernel does not support UFFD_FEATURE_PAGEFAULT_FLAG_WP (features=0x%x)", apiStruct.features)
	}

	// Step 3: mmap a one-page anonymous test region.
	pageSize := uintptr(syscall.Getpagesize())
	testRegion, _, merr := syscall.RawSyscall6(
		syscall.SYS_MMAP,
		0,
		pageSize,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANON|syscall.MAP_PRIVATE,
		^uintptr(0), // fd = -1
		0,
	)
	if merr != 0 {
		return nil, fmt.Errorf("uffd: mmap test region: %w", merr)
	}

	testOk := false
	defer func() {
		if !testOk {
			syscall.RawSyscall(syscall.SYS_MUNMAP, testRegion, pageSize, 0) //nolint:errcheck
		}
	}()

	// Step 4: register with UFFDIO_REGISTER_MODE_WP.
	reg := uffdioRegisterStrategy{
		uffdioRangeStrategy: uffdioRangeStrategy{
			start: uint64(testRegion),
			len:   uint64(pageSize),
		},
		mode: uffdStrategyRegisterMWP,
	}
	if err := ioctlUffd(fd, ioctlUffdioRegisterStrategy, uintptr(unsafe.Pointer(&reg))); err != 0 {
		return nil, fmt.Errorf("uffd: UFFDIO_REGISTER_MODE_WP: %w", err)
	}

	// Step 5: arm write-protection on the test region.
	wp := uffdioWPStrategy{
		uffdioRangeStrategy: uffdioRangeStrategy{
			start: uint64(testRegion),
			len:   uint64(pageSize),
		},
		mode: uffdioWPModeWP,
	}
	if err := ioctlUffd(fd, ioctlUffdioWPStrategy, uintptr(unsafe.Pointer(&wp))); err != 0 {
		// Not fatal — WP arming may fail if UFFDIO_WRITEPROTECT ioctl is not
		// in the registered region's ioctls bitmap on older kernels.
		// Log and continue; dirty tracking will not work but the fd is valid.
		_ = err
	}

	ok = true
	testOk = true

	return &UffdProbeStrategy{
		uffdFd:     int(fd),
		testRegion: testRegion,
		testSize:   pageSize,
		dirtyPages: make(map[int]struct{}),
		fallback:   NewFullMemcpyStrategy(),
	}, nil
}

// Take implements SnapshotStrategy. Delegates to FullMemcpyStrategy because
// wazero does not expose the raw memory address needed to register the WASM
// linear memory region with uffd. See package-level comment for the path to a
// full implementation.
func (u *UffdProbeStrategy) Take(mem api.Memory) error {
	return u.fallback.Take(mem)
}

// Restore implements SnapshotStrategy. Same delegation as Take.
func (u *UffdProbeStrategy) Restore(mem api.Memory) error {
	return u.fallback.Restore(mem)
}

// Close implements SnapshotStrategy. Releases the uffd fd and the test mmap
// region.
func (u *UffdProbeStrategy) Close() error {
	var errs []error

	if u.testRegion != 0 {
		if _, _, errno := syscall.RawSyscall(syscall.SYS_MUNMAP, u.testRegion, u.testSize, 0); errno != 0 {
			errs = append(errs, fmt.Errorf("uffd: munmap test region: %w", errno))
		}
		u.testRegion = 0
	}

	if u.uffdFd >= 0 {
		if err := syscall.Close(u.uffdFd); err != nil {
			errs = append(errs, fmt.Errorf("uffd: close fd: %w", err))
		}
		u.uffdFd = -1
	}

	if err := u.fallback.Close(); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// ---------------------------------------------------------------------------
// UffdStrategy — real dirty-page tracking on WASM linear memory
// ---------------------------------------------------------------------------

// uffdMsg mirrors the kernel struct uffd_msg (linux/userfaultfd.h, __packed).
// Layout (32 bytes total, same on all 64-bit arches):
//
//	 0:  event     uint8           — UFFD_EVENT_PAGEFAULT = 0x12
//	 1:  _         [7]byte         — reserved1(1) + reserved2(2) + reserved3(4)
//	 8:  flags     uint64          — arg.pagefault.flags (UFFD_PAGEFAULT_FLAG_WP etc.)
//	16:  address   uint64          — arg.pagefault.address (faulting page address)
//	24:  _pad      [8]byte         — arg.pagefault.feat.ptid(4) + padding(4)
//
// IMPORTANT: flags is at offset 8, address is at offset 16. Getting this
// wrong causes the faultLoop to read the flags field as the address, which
// produces a garbage address that is always outside the registered region,
// so the fault is silently skipped and the faulting thread remains blocked.
type uffdMsg struct {
	event   uint8
	_       [7]uint8 // reserved1(1) + reserved2(2) + reserved3(4)
	flags   uint64   // arg.pagefault.flags — UFFD_PAGEFAULT_FLAG_WP = 1<<9
	address uint64   // arg.pagefault.address — faulting page address
	_pad    [8]uint8 // arg.pagefault.feat.ptid(4) + padding(4)
}

const (
	uffdEventPagefault = 0x12 // UFFD_EVENT_PAGEFAULT
	uffdPagfaultFlagWP = 1 << 9 // UFFD_PAGEFAULT_FLAG_WP
)

// UffdStrategy is a full dirty-page tracking SnapshotStrategy backed by Linux
// userfaultfd write-protect mode. It registers the WASM linear memory region
// directly with uffd and tracks which pages were written between Take and
// Restore. On Restore it copies back only the dirty pages, amortising restore
// cost for large modules where only a small fraction of pages are written per
// request.
//
// How it obtains the raw WASM memory pointer:
//   - wazero's api.Memory.Read(0, size) returns m.Buffer[0:size] — a direct
//     slice into the backing store, not a copy.
//   - unsafe.SliceData on that slice gives the address of m.Buffer[0].
//   - wazero allocates linear memory via make([]byte, N) in NewMemoryInstance.
//   - For large N, Go's runtime uses mmap(MAP_ANON|MAP_PRIVATE) internally.
//   - MAP_PRIVATE satisfies the kernel's requirement for UFFDIO_REGISTER_MODE_WP.
//   - Therefore UFFDIO_REGISTER succeeds on wazero linear memory without any
//     wazero fork or custom allocator.
//
// Memory growth is prevented by configuring WithMemoryLimitPages in the wazero
// runtime, so the registration remains valid for the lifetime of the module.
type UffdStrategy struct {
	uffdFd    int
	basePtr   unsafe.Pointer
	memSize   uint32
	pageSize  int
	pageCount int

	snapshot []byte // captured by Take
	mu       sync.Mutex
	dirty    []bool // dirty[i] = true if page i was written since last Take

	closeOnce sync.Once
	stopCh    chan struct{} // closed to stop faultLoop
	doneCh    chan struct{} // closed when faultLoop exits
}

// NewUffdStrategy creates a UffdStrategy for the given WASM module memory.
// It obtains the raw backing pointer from mem.Read, registers the region with
// uffd in WP mode, arms write-protection, and starts a background fault-
// handler goroutine.
func NewUffdStrategy(mem api.Memory) (*UffdStrategy, error) {
	if mem == nil {
		return nil, fmt.Errorf("uffd: nil api.Memory")
	}

	size := mem.Size()
	if size == 0 {
		return nil, fmt.Errorf("uffd: zero-size linear memory")
	}

	// Obtain the raw backing pointer. On wazero/linux this slice is backed
	// directly by the mmap region for linear memory. unsafe.SliceData gives us
	// the address of the first element without making a copy.
	buf, ok := mem.Read(0, size)
	if !ok {
		return nil, fmt.Errorf("uffd: could not read linear memory (size=%d)", size)
	}
	basePtr := unsafe.Pointer(unsafe.SliceData(buf))

	pageSize := syscall.Getpagesize()
	pageCount := int((uint64(size) + uint64(pageSize) - 1) / uint64(pageSize))

	// Open uffd fd without O_NONBLOCK: faultLoop uses blocking syscall.Read,
	// so the fd must be in blocking mode. O_NONBLOCK would cause Read to return
	// EAGAIN immediately instead of waiting for the next fault event.
	fd, _, errno := syscall.RawSyscall(
		uffdSyscallNr,
		uffdOCloexecStrategy,
		0, 0,
	)
	if errno != 0 {
		return nil, fmt.Errorf("uffd: userfaultfd syscall: %w", errno)
	}

	cleanup := true
	defer func() {
		if cleanup {
			syscall.Close(int(fd)) //nolint:errcheck
		}
	}()

	// UFFDIO_API handshake — request WP feature.
	apiStruct := uffdioAPIStructStrategy{
		api:      uffdStrategyAPI,
		features: uffdStrategyFeatureWP, // request WP support
	}
	if err := ioctlUffd(fd, ioctlUffdioAPIStrategy, uintptr(unsafe.Pointer(&apiStruct))); err != 0 {
		return nil, fmt.Errorf("uffd: UFFDIO_API handshake: %w", err)
	}
	if apiStruct.features&uffdStrategyFeatureWP == 0 {
		return nil, fmt.Errorf("uffd: kernel does not support UFFD_FEATURE_PAGEFAULT_FLAG_WP (features=0x%x)", apiStruct.features)
	}

	// UFFDIO_REGISTER the WASM linear memory with WP mode.
	reg := uffdioRegisterStrategy{
		uffdioRangeStrategy: uffdioRangeStrategy{
			start: uint64(uintptr(basePtr)),
			len:   uint64(size),
		},
		mode: uffdStrategyRegisterMWP,
	}
	if err := ioctlUffd(fd, ioctlUffdioRegisterStrategy, uintptr(unsafe.Pointer(&reg))); err != 0 {
		return nil, fmt.Errorf("uffd: UFFDIO_REGISTER_MODE_WP on wasm memory: %w", err)
	}

	// Arm write-protection on the entire region.
	wp := uffdioWPStrategy{
		uffdioRangeStrategy: uffdioRangeStrategy{
			start: uint64(uintptr(basePtr)),
			len:   uint64(size),
		},
		mode: uffdioWPModeWP,
	}
	if err := ioctlUffd(fd, ioctlUffdioWPStrategy, uintptr(unsafe.Pointer(&wp))); err != 0 {
		return nil, fmt.Errorf("uffd: UFFDIO_WRITEPROTECT arm on wasm memory: %w", err)
	}

	s := &UffdStrategy{
		uffdFd:    int(fd),
		basePtr:   basePtr,
		memSize:   size,
		pageSize:  pageSize,
		pageCount: pageCount,
		snapshot:  make([]byte, size),
		dirty:     make([]bool, pageCount),
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}

	cleanup = false // fd ownership transferred to s

	go s.faultLoop()

	return s, nil
}

// faultLoop runs in a dedicated goroutine. It blocks reading uffd_msg structs
// from the uffd fd. For each write-protect fault it:
//  1. Marks the faulting page as dirty.
//  2. Disarms WP for that page so the faulting thread can proceed.
//
// The loop exits when the fd is closed (read returns an error).
func (s *UffdStrategy) faultLoop() {
	defer close(s.doneCh)

	var msg uffdMsg
	msgSize := unsafe.Sizeof(msg)
	msgBuf := (*[unsafe.Sizeof(uffdMsg{})]byte)(unsafe.Pointer(&msg))

	for {
		// Block-read one uffd_msg. The fd is in blocking mode (no O_NONBLOCK),
		// so syscall.Read will wait until a fault event arrives.
		// When the fd is closed, Read returns an error and we exit.
		n, err := syscall.Read(s.uffdFd, msgBuf[:msgSize])
		if err != nil || n == 0 {
			// fd closed or error — exit cleanly.
			return
		}
		if uint(n) < uint(msgSize) {
			// Short read — shouldn't happen on uffd but be defensive.
			continue
		}

		if msg.event != uffdEventPagefault {
			// Not a pagefault event — skip (e.g. fork/remap events).
			continue
		}

		faultAddr := uintptr(msg.address)
		base := uintptr(s.basePtr)

		if faultAddr < base || faultAddr >= base+uintptr(s.memSize) {
			// Fault outside our region — shouldn't happen, skip.
			continue
		}

		pageIdx := int((faultAddr - base) / uintptr(s.pageSize))
		// Align faultAddr down to page boundary.
		pageBase := base + uintptr(pageIdx)*uintptr(s.pageSize)

		// Mark page dirty.
		s.mu.Lock()
		if pageIdx < len(s.dirty) {
			s.dirty[pageIdx] = true
		}
		s.mu.Unlock()

		// Disarm WP for this page so the faulting thread can proceed.
		wp := uffdioWPStrategy{
			uffdioRangeStrategy: uffdioRangeStrategy{
				start: uint64(pageBase),
				len:   uint64(s.pageSize),
			},
			mode: 0, // clear WP
		}
		ioctlUffd(uintptr(s.uffdFd), ioctlUffdioWPStrategy, uintptr(unsafe.Pointer(&wp))) //nolint:errcheck
	}
}

// Take implements SnapshotStrategy. It copies the current WASM linear memory
// into the internal snapshot buffer, re-arms write-protection on the entire
// region, and clears the dirty page bitset.
func (s *UffdStrategy) Take(mem api.Memory) error {
	if mem == nil {
		return nil
	}

	// Reads don't trigger WP faults — we can read directly without disarming.
	buf := unsafe.Slice((*byte)(s.basePtr), s.memSize)

	s.mu.Lock()
	copy(s.snapshot, buf)
	for i := range s.dirty {
		s.dirty[i] = false
	}
	s.mu.Unlock()

	// Re-arm WP on the entire region (disarm may have been done for dirty pages
	// during a previous Restore that didn't re-arm, or after initial setup).
	wp := uffdioWPStrategy{
		uffdioRangeStrategy: uffdioRangeStrategy{
			start: uint64(uintptr(s.basePtr)),
			len:   uint64(s.memSize),
		},
		mode: uffdioWPModeWP,
	}
	if err := ioctlUffd(uintptr(s.uffdFd), ioctlUffdioWPStrategy, uintptr(unsafe.Pointer(&wp))); err != 0 {
		return fmt.Errorf("uffd: Take: re-arm WP: %w", err)
	}

	return nil
}

// Restore implements SnapshotStrategy. It writes snapshot data back to only
// the pages that were dirtied since the last Take, then re-arms WP on those
// pages and clears the dirty bitset.
func (s *UffdStrategy) Restore(mem api.Memory) error {
	if mem == nil || s.snapshot == nil {
		return nil
	}

	s.mu.Lock()
	// Collect dirty page indices under the lock, then release.
	var dirtyPages []int
	for i, d := range s.dirty {
		if d {
			dirtyPages = append(dirtyPages, i)
			s.dirty[i] = false
		}
	}
	s.mu.Unlock()

	if len(dirtyPages) == 0 {
		return nil
	}

	pageSize := s.pageSize

	for _, pg := range dirtyPages {
		off := uint32(pg * pageSize)
		end := off + uint32(pageSize)
		if end > s.memSize {
			end = s.memSize
		}

		// Restore page from snapshot using mem.Write (goes through wazero bounds check).
		if !mem.Write(off, s.snapshot[off:end]) {
			return fmt.Errorf("uffd: Restore: mem.Write failed at offset %d", off)
		}

		// Re-arm WP on this page.
		wp := uffdioWPStrategy{
			uffdioRangeStrategy: uffdioRangeStrategy{
				start: uint64(uintptr(s.basePtr)) + uint64(off),
				len:   uint64(end - off),
			},
			mode: uffdioWPModeWP,
		}
		if err := ioctlUffd(uintptr(s.uffdFd), ioctlUffdioWPStrategy, uintptr(unsafe.Pointer(&wp))); err != 0 {
			return fmt.Errorf("uffd: Restore: re-arm WP for page %d: %w", pg, err)
		}
	}

	return nil
}

// Close implements SnapshotStrategy. It disarms write-protection, stops the
// fault-handler goroutine by closing the uffd fd, and waits for it to exit.
func (s *UffdStrategy) Close() error {
	var retErr error
	s.closeOnce.Do(func() {
		// Disarm WP on the entire region so the memory is usable after close.
		wp := uffdioWPStrategy{
			uffdioRangeStrategy: uffdioRangeStrategy{
				start: uint64(uintptr(s.basePtr)),
				len:   uint64(s.memSize),
			},
			mode: 0,
		}
		ioctlUffd(uintptr(s.uffdFd), ioctlUffdioWPStrategy, uintptr(unsafe.Pointer(&wp))) //nolint:errcheck

		// Closing the fd causes faultLoop's Read to return an error, stopping it.
		if err := syscall.Close(s.uffdFd); err != nil {
			retErr = fmt.Errorf("uffd: close fd: %w", err)
		}
		s.uffdFd = -1

		// Wait for faultLoop to exit.
		<-s.doneCh
	})
	return retErr
}

// NewSnapshotStrategy returns a UffdProbeStrategy if userfaultfd WP mode is
// available, otherwise falls back to FullMemcpyStrategy.
func NewSnapshotStrategy() SnapshotStrategy {
	s, err := NewUffdProbeStrategy()
	if err != nil {
		return NewFullMemcpyStrategy()
	}
	return s
}

// UffdAvailable reports whether the userfaultfd syscall is permitted in this
// environment. Under Docker's default seccomp profile the syscall returns EPERM.
func UffdAvailable() bool {
	fd, _, errno := syscall.RawSyscall(uffdSyscallNr, 0, 0, 0)
	if errno != 0 {
		return false
	}
	syscall.Close(int(fd)) //nolint:errcheck
	return true
}


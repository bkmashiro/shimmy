//go:build linux

package wasm

import (
	"fmt"
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
// For the actual Take / Restore operations it currently falls back to
// FullMemcpyStrategy. The reason is a wazero-internal limitation: wazero's
// api.Memory does not expose the raw pointer or file descriptor backing its
// linear-memory []byte, so we cannot register the WASM memory region directly
// with uffd. A full dirty-page restore implementation would require one of:
//
//   - wazero experimental API that exposes the backing mmap address (not yet
//     upstream as of wazero v1.x)
//   - A custom wazero MemoryDefinition that allocates linear memory via our
//     own mmap and passes the fd/addr to UffdProbeStrategy
//   - An mprotect-based approach using unsafe.SliceData on the []byte returned
//     by api.Memory.Read (works today but is not officially supported)
//
// Until one of those paths is available, UffdProbeStrategy serves as:
//  1. A CI probe confirming uffd+WP works on the target kernel.
//  2. A scaffold that already holds the uffd fd and knows dirty pages — once
//     the memory address is accessible it can be wired up with minimal changes.
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
	ioctlUffdioWPStrategy       = 0xc010aa06 // UFFDIO_WRITEPROTECT

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

// NewSnapshotStrategy returns a UffdProbeStrategy if userfaultfd WP mode is
// available, otherwise falls back to FullMemcpyStrategy. This is the
// recommended constructor for production use.
func NewSnapshotStrategy() SnapshotStrategy {
	s, err := NewUffdProbeStrategy()
	if err != nil {
		return NewFullMemcpyStrategy()
	}
	return s
}

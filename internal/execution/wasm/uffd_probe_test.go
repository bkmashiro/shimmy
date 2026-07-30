//go:build linux
// +build linux

package wasm

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// userfaultfd probe tests
//
// These tests verify whether the host kernel / container configuration permits
// the userfaultfd(2) syscall and, if so, whether UFFDIO_REGISTER_MODE_WP
// (write-protect tracking) is available.  The results determine whether the
// dirty-page restore optimisation can be used for large WASM modules such as
// CPython-WASI (26 MB).
//
// Run via:
//   go test ./internal/execution/wasm/... -run TestUserfaultfd -v
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Constants — userfaultfd API
// ---------------------------------------------------------------------------

// userfaultfd(2) syscall number on x86-64 Linux.
// sysUserfaultfd is already declared in bench_test.go (same package), but to
// keep this file self-contained and avoid duplicate-symbol errors when only
// this file is compiled, we reference the package-level constant directly.
// (Go allows multiple files in a package to share constants.)

// O_CLOEXEC / O_NONBLOCK flags for userfaultfd(2).
const (
	uffdOCloexec  = 0x80000 // syscall.O_CLOEXEC on linux/amd64
	uffdONonblock = 0x800   // syscall.O_NONBLOCK on linux/amd64
)

// UFFD API version handshake.
const (
	uffdioAPI     = 0xaa   // _UFFDIO_API magic
	uffdFeatureWP = 1 << 2 // UFFD_FEATURE_PAGEFAULT_FLAG_WP
)

// ioctl request numbers (linux/userfaultfd.h, x86-64).
// _IOWR('AA', 0x3F, struct uffdio_api)    → UFFDIO_API
// _IOWR('AA', 0x00, struct uffdio_register) → UFFDIO_REGISTER
const (
	ioctlUffdioAPI      = 0xc018aa3f // UFFDIO_API
	ioctlUffdioRegister = 0xc020aa00 // UFFDIO_REGISTER
)

// UFFDIO_REGISTER_MODE_WP — register region for write-protect tracking.
const uffdioRegisterModeWP = 1 << 1

// ---------------------------------------------------------------------------
// C-layout structs (must match kernel ABI exactly)
// ---------------------------------------------------------------------------

// uffdioAPI mirrors struct uffdio_api.
type uffdioAPIStruct struct {
	api      uint64 // in: API version (UFFD_API = 0xaa)
	features uint64 // in/out: feature flags
	ioctls   uint64 // out: available ioctls
}

// uffdioRange mirrors struct uffdio_range.
type uffdioRange struct {
	start uint64
	len   uint64
}

// uffdioRegister mirrors struct uffdio_register.
type uffdioRegister struct {
	uffdioRange        // embedded range
	mode        uint64 // in: UFFDIO_REGISTER_MODE_*
	ioctls      uint64 // out: available ioctls for this region
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func ioctl(fd uintptr, req uint, arg uintptr) (errno syscall.Errno) {
	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(req), arg)
	return
}

// openUserfaultfd creates a userfaultfd file descriptor.
// Returns (fd, errno).
func openUserfaultfd() (uintptr, syscall.Errno) {
	fd, _, errno := syscall.RawSyscall(
		sysUserfaultfd,
		uffdOCloexec|uffdONonblock,
		0, 0,
	)
	return fd, errno
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestUserfaultfdProbe checks whether userfaultfd is available and whether
// UFFDIO_REGISTER_MODE_WP can be used for dirty-page tracking.
func TestUserfaultfdProbe(t *testing.T) {
	// -------------------------------------------------------------------
	// Test 1: fd creation
	// -------------------------------------------------------------------
	fd, errno := openUserfaultfd()
	if errno != 0 {
		t.Logf("userfaultfd syscall failed: errno=%v (%s)", errno, errno.Error())
		t.Logf("Possible causes: container seccomp filter, missing CAP_SYS_PTRACE, kernel < 4.3")
		t.Skip("userfaultfd not available in this environment")
	}
	t.Log("userfaultfd: fd creation OK")
	defer syscall.Close(int(fd))

	// -------------------------------------------------------------------
	// Test 2: UFFDIO_API handshake
	// -------------------------------------------------------------------
	apiStruct := uffdioAPIStruct{
		api:      uffdioAPI,
		features: 0,
	}
	if err := ioctl(fd, ioctlUffdioAPI, uintptr(unsafe.Pointer(&apiStruct))); err != 0 {
		t.Fatalf("UFFDIO_API ioctl failed: %v", err)
	}
	t.Logf("UFFDIO_API: OK (features=0x%x, ioctls=0x%x)",
		apiStruct.features, apiStruct.ioctls)

	// Check whether the kernel advertises write-protect support.
	if apiStruct.features&uffdFeatureWP == 0 {
		t.Log("UFFDIO_REGISTER_MODE_WP: kernel does not advertise UFFD_FEATURE_PAGEFAULT_FLAG_WP")
		t.Log("Dirty-page tracking via write-protect is NOT available on this kernel")
		return
	}
	t.Log("UFFD_FEATURE_PAGEFAULT_FLAG_WP: advertised by kernel")

	// -------------------------------------------------------------------
	// Test 3: mmap a region and register it with UFFDIO_REGISTER_MODE_WP
	// -------------------------------------------------------------------
	pageSize := syscall.Getpagesize()
	regionSize := uintptr(pageSize * 4)

	// MAP_ANONYMOUS | MAP_PRIVATE
	addr, _, merr := syscall.RawSyscall6(
		syscall.SYS_MMAP,
		0, // let kernel choose address
		regionSize,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANON|syscall.MAP_PRIVATE,
		^uintptr(0), // fd = -1
		0,
	)
	if merr != 0 {
		t.Fatalf("mmap failed: %v", merr)
	}
	defer syscall.RawSyscall(syscall.SYS_MUNMAP, addr, regionSize, 0) //nolint:errcheck

	reg := uffdioRegister{
		uffdioRange: uffdioRange{
			start: uint64(addr),
			len:   uint64(regionSize),
		},
		mode: uffdioRegisterModeWP,
	}

	if err := ioctl(fd, ioctlUffdioRegister, uintptr(unsafe.Pointer(&reg))); err != 0 {
		t.Logf("UFFDIO_REGISTER_MODE_WP: FAIL (errno=%v — %s)", err, err.Error())
		t.Logf("Dirty-page tracking via write-protect is NOT available (seccomp or kernel restriction)")
		// Not fatal — report, don't fail. The benchmark will be skipped.
		return
	}

	t.Logf("UFFDIO_REGISTER_MODE_WP: OK (region ioctls=0x%x)", reg.ioctls)
	t.Log("userfaultfd dirty-page tracking is AVAILABLE in this environment")
}

// TestUserfaultfdProbe_FdOnly is a minimal sub-probe that only checks fd
// creation — useful for quick CI smoke tests on kernels without WP support.
func TestUserfaultfdProbe_FdOnly(t *testing.T) {
	fd, errno := openUserfaultfd()
	if errno != 0 {
		t.Logf("userfaultfd syscall failed: errno=%v (%s)", errno, errno.Error())
		t.Skip("userfaultfd not available in this environment (seccomp or insufficient privileges)")
	}
	require.Zero(t, errno, "userfaultfd syscall failed: errno=%v", errno)
	t.Log("userfaultfd: fd creation OK")
	syscall.Close(int(fd))
}

// ---------------------------------------------------------------------------
// TestUffdProbe_GoMakeSlice
// ---------------------------------------------------------------------------

// TestUffdProbe_GoMakeSlice specifically tests whether a large Go slice
// allocated with make([]byte, N) is registerable with UFFDIO_REGISTER_MODE_WP.
//
// This is the critical question for wazero linear memory compatibility: wazero
// allocates linear memory via make([]byte, N) in internal/wasm/memory.go, so
// if this test succeeds, UffdStrategy should be able to register the WASM
// linear memory with uffd.
//
// Background on wazero linear memory allocation:
//   - wazero calls make([]byte, minBytes, capBytes) in NewMemoryInstance
//   - For large allocations Go's runtime uses mmap(MAP_ANON|MAP_PRIVATE)
//   - MAP_PRIVATE is required for UFFDIO_REGISTER_MODE_WP
//   - The old MAP_SHARED hypothesis appears to be
//     incorrect — wazero does NOT use MAP_SHARED for linear memory
//
// If this test fails with EINVAL, the VMA backing the slice may:
//   - Span multiple VMAs (Go heap arena boundary)
//   - Be allocated from a range with VM_SHARED (unexpected for Go)
//   - Have start address that is not page-aligned (log the offset)
func TestUffdProbe_GoMakeSlice(t *testing.T) {
	if !UffdAvailable() {
		t.Skip("userfaultfd not available (seccomp or insufficient privileges)")
	}

	// Open uffd and perform API handshake with WP feature.
	fd, errno := openUserfaultfd()
	require.Zero(t, errno, "userfaultfd syscall failed")
	defer syscall.Close(int(fd))

	apiStruct := uffdioAPIStruct{
		api:      uffdioAPI,
		features: uffdFeatureWP,
	}
	require.Zero(t, ioctl(fd, ioctlUffdioAPI, uintptr(unsafe.Pointer(&apiStruct))),
		"UFFDIO_API handshake failed")

	if apiStruct.features&uffdFeatureWP == 0 {
		t.Skip("UFFD_FEATURE_PAGEFAULT_FLAG_WP not available — skipping")
	}

	pageSize := syscall.Getpagesize()

	// Test several sizes, including 26MB (CPython WASM heap size).
	sizes := []struct {
		label string
		size  int
	}{
		{"64KB (1 WASM page)", 64 * 1024},
		{"4MB (small WASM module)", 4 * 1024 * 1024},
		{"26MB (CPython WASM heap)", 26 * 1024 * 1024},
	}

	for _, tc := range sizes {
		t.Run(tc.label, func(t *testing.T) {
			// Allocate via Go's make([]byte, N) — this is exactly what wazero does
			// for WASM linear memory in internal/wasm/memory.go.
			buf := make([]byte, tc.size)

			// Get the raw pointer to the underlying array.
			baseAddr := uintptr(unsafe.Pointer(unsafe.SliceData(buf)))

			t.Logf("make([]byte, %d): base=0x%x", tc.size, baseAddr)

			// Check page alignment.
			if baseAddr%uintptr(pageSize) != 0 {
				t.Logf("WARNING: base address is NOT page-aligned (offset=%d)",
					baseAddr%uintptr(pageSize))
				t.Logf("Adjusting to nearest page boundary for registration test")
				// Align up for test (real wazero memory must be aligned).
				aligned := (baseAddr + uintptr(pageSize) - 1) &^ (uintptr(pageSize) - 1)
				regSize := uintptr(tc.size) - (aligned - baseAddr)
				regSize = regSize &^ (uintptr(pageSize) - 1)
				baseAddr = aligned
				tc.size = int(regSize)
			}

			// Show the VMA backing the slice (from /proc/self/maps).
			showVMAForAddr(t, baseAddr)

			// Attempt UFFDIO_REGISTER_MODE_WP.
			reg := uffdioRegister{
				uffdioRange: uffdioRange{
					start: uint64(baseAddr),
					len:   uint64(tc.size),
				},
				mode: uffdioRegisterModeWP,
			}

			err := ioctl(fd, ioctlUffdioRegister, uintptr(unsafe.Pointer(&reg)))
			if err != 0 {
				t.Logf("UFFDIO_REGISTER_MODE_WP FAILED: errno=%v (%s)", err, err.Error())
				t.Logf("This means wazero linear memory CANNOT be registered with uffd WP")
				t.Logf("UffdStrategy will need a workaround (custom allocator or mprotect fallback)")
				// Not fatal — this is a probe, not a hard requirement.
				return
			}

			t.Logf("UFFDIO_REGISTER_MODE_WP: OK (ioctls=0x%x)", reg.ioctls)
			t.Log("Go make([]byte) IS compatible with uffd WP — UffdStrategy should work")

			// Unregister to clean up.
			unregRange := uffdioRange{
				start: uint64(baseAddr),
				len:   uint64(tc.size),
			}
			_ = ioctl(fd, 0x8010aa01 /* UFFDIO_UNREGISTER */, uintptr(unsafe.Pointer(&unregRange)))

			// Keep buf alive until after ioctl.
			_ = buf
		})
	}
}

// showVMAForAddr logs the VMA line from /proc/self/maps that contains addr.
func showVMAForAddr(t *testing.T, addr uintptr) {
	t.Helper()
	data, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if len(line) == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		parts := strings.SplitN(fields[0], "-", 2)
		if len(parts) != 2 {
			continue
		}
		var start, end uint64
		if _, err2 := fmt.Sscanf(parts[0], "%x", &start); err2 != nil {
			continue
		}
		if _, err2 := fmt.Sscanf(parts[1], "%x", &end); err2 != nil {
			continue
		}
		if uintptr(start) <= addr && addr < uintptr(end) {
			t.Logf("VMA containing 0x%x: %s", addr, line)
			return
		}
	}
	t.Logf("VMA for 0x%x: not found in /proc/self/maps", addr)
}

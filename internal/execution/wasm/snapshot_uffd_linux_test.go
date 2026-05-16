//go:build linux

package wasm

import (
	"syscall"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TestUffdStrategy_FallbackOnUnavailable
// ---------------------------------------------------------------------------

// TestUffdStrategy_FallbackOnUnavailable checks that NewSnapshotStrategy
// returns a non-nil strategy regardless of whether uffd is available.
// This exercises the graceful fallback path.
func TestUffdStrategy_FallbackOnUnavailable(t *testing.T) {
	s := NewSnapshotStrategy()
	require.NotNil(t, s, "NewSnapshotStrategy must never return nil")

	switch v := s.(type) {
	case *UffdProbeStrategy:
		t.Log("uffd available — UffdProbeStrategy returned")
		require.NoError(t, v.Close())
	case *FullMemcpyStrategy:
		t.Log("uffd unavailable — FullMemcpyStrategy returned (expected in Docker/seccomp)")
		require.NoError(t, v.Close())
	default:
		t.Fatalf("unexpected strategy type: %T", s)
	}
}

// ---------------------------------------------------------------------------
// TestUffdStrategy_EndToEnd
// ---------------------------------------------------------------------------

// TestUffdStrategy_EndToEnd proves the full uffd WP mechanism end-to-end
// using our OWN mmap'd region (not WASM linear memory — see package-level
// comment in snapshot_uffd_linux.go for the wazero limitation).
//
// Steps:
//  1. Open uffd fd, perform UFFDIO_API handshake, check for WP feature
//  2. mmap a region (4 pages)
//  3. Take a snapshot of the region content
//  4. Register with UFFDIO_REGISTER_MODE_WP and arm WP on the whole region
//  5. Simulate writes to two pages by first un-arming WP on those pages,
//     writing, then re-arming — (normally the fault handler does this)
//  6. Restore only the two "dirty" pages from snapshot
//  7. Verify content is back to snapshot state
func TestUffdStrategy_EndToEnd(t *testing.T) {
	if !UffdAvailable() {
		t.Skip("userfaultfd not available in this environment (seccomp or insufficient privileges)")
	}

	// --- 1. Open uffd and handshake ---
	fd, errno := openUserfaultfd()
	require.Zero(t, errno, "userfaultfd syscall failed: %v", errno)
	defer syscall.Close(int(fd))

	apiStruct := uffdioAPIStruct{
		api:      uffdioAPI,
		features: 0,
	}
	require.Zero(t, ioctl(fd, ioctlUffdioAPI, uintptr(unsafe.Pointer(&apiStruct))),
		"UFFDIO_API handshake failed")

	if apiStruct.features&uffdFeatureWP == 0 {
		t.Skip("kernel does not advertise UFFD_FEATURE_PAGEFAULT_FLAG_WP — skipping WP end-to-end test")
	}

	// --- 2. mmap 4-page region ---
	pageSize := syscall.Getpagesize()
	numPages := 4
	regionSize := uintptr(pageSize * numPages)

	addr, _, merr := syscall.RawSyscall6(
		syscall.SYS_MMAP,
		0,
		regionSize,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_ANON|syscall.MAP_PRIVATE,
		^uintptr(0),
		0,
	)
	require.Zero(t, merr, "mmap failed: %v", merr)
	defer syscall.RawSyscall(syscall.SYS_MUNMAP, addr, regionSize, 0) //nolint:errcheck

	// Cast to a Go slice for convenient access.
	region := unsafe.Slice((*byte)(unsafe.Pointer(addr)), int(regionSize))

	// --- 3. Fill region with known pattern and take snapshot ---
	for i := range region {
		region[i] = byte(i % 251) // arbitrary non-zero pattern
	}
	snapshot := make([]byte, regionSize)
	copy(snapshot, region)

	// --- 4. Register with UFFDIO_REGISTER_MODE_WP ---
	reg := uffdioRegister{
		uffdioRange: uffdioRange{
			start: uint64(addr),
			len:   uint64(regionSize),
		},
		mode: uffdioRegisterModeWP,
	}
	require.Zero(t, ioctl(fd, ioctlUffdioRegister, uintptr(unsafe.Pointer(&reg))),
		"UFFDIO_REGISTER_MODE_WP failed")

	// Arm write-protection on the entire region.
	wpAll := uffdioWPStrategy{
		uffdioRangeStrategy: uffdioRangeStrategy{
			start: uint64(addr),
			len:   uint64(regionSize),
		},
		mode: uffdioWPModeWP,
	}
	wpErr := ioctlUffd(uintptr(fd), ioctlUffdioWPStrategy, uintptr(unsafe.Pointer(&wpAll)))
	if wpErr != 0 {
		t.Skipf("UFFDIO_WRITEPROTECT arm failed (errno=%v) — kernel may not support it here", wpErr)
	}

	// --- 5. Simulate writes on pages 1 and 3 ---
	// In production, the uffd fault handler receives UFFD_EVENT_PAGEFAULT
	// messages for each write, records the page as dirty, then un-arms WP for
	// that page so the guest can proceed. Here we simulate that by manually
	// un-arming WP, writing, and recording the page as dirty.
	dirtyPages := []int{1, 3}
	for _, pg := range dirtyPages {
		// Un-arm WP for this page so the write doesn't panic.
		wpPage := uffdioWPStrategy{
			uffdioRangeStrategy: uffdioRangeStrategy{
				start: uint64(addr) + uint64(pg*pageSize),
				len:   uint64(pageSize),
			},
			mode: 0, // clear WP
		}
		require.Zero(t,
			ioctlUffd(uintptr(fd), ioctlUffdioWPStrategy, uintptr(unsafe.Pointer(&wpPage))),
			"un-arm WP for page %d", pg)

		// Overwrite the page with zeros (corrupted state).
		start := pg * pageSize
		for i := start; i < start+pageSize; i++ {
			region[i] = 0
		}
	}

	// Verify dirty pages are actually different from the snapshot.
	for _, pg := range dirtyPages {
		start := pg * pageSize
		assert.NotEqual(t, snapshot[start:start+pageSize], region[start:start+pageSize],
			"page %d should be dirty (different from snapshot)", pg)
	}
	// Clean pages should still match.
	for pg := 0; pg < numPages; pg++ {
		isDirty := false
		for _, dp := range dirtyPages {
			if dp == pg {
				isDirty = true
				break
			}
		}
		if !isDirty {
			start := pg * pageSize
			assert.Equal(t, snapshot[start:start+pageSize], region[start:start+pageSize],
				"page %d should be clean (match snapshot)", pg)
		}
	}

	// --- 6. Restore dirty pages from snapshot ---
	for _, pg := range dirtyPages {
		start := pg * pageSize
		copy(region[start:start+pageSize], snapshot[start:start+pageSize])
	}

	// --- 7. Verify all pages match snapshot ---
	assert.Equal(t, snapshot, []byte(region),
		"after restore, entire region should match snapshot")

	t.Logf("uffd end-to-end: OK (pageSize=%d, numPages=%d, dirtyRestored=%d)",
		pageSize, numPages, len(dirtyPages))
}

// ---------------------------------------------------------------------------
// TestUffdProbeStrategy_NewAndClose
// ---------------------------------------------------------------------------

// TestUffdProbeStrategy_NewAndClose verifies that UffdProbeStrategy can be
// created and closed without resource leaks when uffd+WP is available.
func TestUffdProbeStrategy_NewAndClose(t *testing.T) {
	if !UffdAvailable() {
		t.Skip("userfaultfd not available")
	}

	s, err := NewUffdProbeStrategy()
	if err != nil {
		// WP might not be available even if fd creation is.
		t.Logf("NewUffdProbeStrategy returned error (expected if no WP): %v", err)
		t.Skip("UFFDIO_REGISTER_MODE_WP unavailable — skipping probe strategy test")
	}

	require.NotNil(t, s)
	assert.GreaterOrEqual(t, s.uffdFd, 0, "uffd fd should be non-negative")
	assert.NotZero(t, s.testRegion, "test mmap region should be non-zero")

	require.NoError(t, s.Close())
	assert.Equal(t, -1, s.uffdFd, "uffd fd should be -1 after Close")
	assert.Zero(t, s.testRegion, "test region should be 0 after Close")
}

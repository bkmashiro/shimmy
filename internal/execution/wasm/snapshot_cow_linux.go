//go:build linux

package wasm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/tetratelabs/wazero/experimental"
	"golang.org/x/sys/unix"
)

// cowImage is the immutable dispatcher-scoped prepared linear-memory image.
// The coordinator owns its file descriptor. Per-instance mappings remain valid
// after a descriptor close, but reset/replacement needs the descriptor, so the
// coordinator must outlive every attached module.
type cowImage struct {
	fd     int
	size   uint64
	digest [sha256.Size]byte
	closed atomic.Bool
}

func (i *cowImage) ID() string {
	if i == nil {
		return ""
	}
	return hex.EncodeToString(i.digest[:])
}

// cowImageCoordinator intentionally owns one image for one dispatcher. It is
// not a process-global cache: module/evaluator/config identity is already bound
// by the dispatcher that creates it.
type cowImageCoordinator struct {
	mu     sync.Mutex
	image  *cowImage
	closed bool
}

func newCowImageCoordinator() *cowImageCoordinator {
	return &cowImageCoordinator{}
}

func (c *cowImageCoordinator) ImageID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.image.ID()
}

// PublishOrAttach publishes the first prepared memory as the canonical sealed
// image. Later memories attach only when size and digest match exactly.
func (c *cowImageCoordinator) PublishOrAttach(memory *cowLinearMemory) error {
	if memory == nil {
		return fmt.Errorf("%w: nil linear memory", ErrCowUnavailable)
	}

	buf := memory.Bytes()
	if len(buf) == 0 {
		return fmt.Errorf("%w: empty linear memory", ErrCowUnavailable)
	}
	digest := sha256.Sum256(buf)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrCowImageClosed
	}

	if c.image == nil {
		image, err := newCowImage(buf, digest)
		if err != nil {
			return err
		}
		if err := memory.attach(image); err != nil {
			_ = unix.Close(image.fd)
			image.closed.Store(true)
			return err
		}
		c.image = image
		return nil
	}

	if c.image.size != uint64(len(buf)) || c.image.digest != digest {
		return fmt.Errorf("%w: canonical_size=%d candidate_size=%d canonical_sha256=%s candidate_sha256=%s",
			ErrCowBaselineMismatch,
			c.image.size,
			len(buf),
			c.image.ID(),
			hex.EncodeToString(digest[:]),
		)
	}
	return memory.attach(c.image)
}

func (c *cowImageCoordinator) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.image == nil {
		return nil
	}
	c.image.closed.Store(true)
	err := unix.Close(c.image.fd)
	c.image.fd = -1
	return err
}

func newCowImage(buf []byte, digest [sha256.Size]byte) (*cowImage, error) {
	fd, err := unix.MemfdCreate("shimmy-cow-image", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, fmt.Errorf("%w: memfd_create: %v", ErrCowUnavailable, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = unix.Close(fd)
		}
	}()

	if err := unix.Ftruncate(fd, int64(len(buf))); err != nil {
		return nil, fmt.Errorf("cow: size canonical image: %w", err)
	}
	for offset := 0; offset < len(buf); {
		n, writeErr := unix.Pwrite(fd, buf[offset:], int64(offset))
		if writeErr != nil {
			return nil, fmt.Errorf("cow: write canonical image at %d: %w", offset, writeErr)
		}
		if n == 0 {
			return nil, fmt.Errorf("cow: write canonical image at %d made no progress", offset)
		}
		offset += n
	}

	seals := unix.F_SEAL_SEAL | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, seals); err != nil {
		return nil, fmt.Errorf("cow: seal canonical image: %w", err)
	}

	ok = true
	return &cowImage{fd: fd, size: uint64(len(buf)), digest: digest}, nil
}

// cowLinearMemory owns one stable virtual address range. Before attachment the
// range is anonymous so normal module initialisation and trusted preparation can
// mutate it. After attachment the visible prefix is a writable MAP_PRIVATE view
// of the immutable canonical image.
type cowLinearMemory struct {
	mu sync.Mutex

	region   []byte
	length   uint64
	capacity uint64
	image    *cowImage
	freed    bool
}

var _ experimental.LinearMemory = (*cowLinearMemory)(nil)

func newCowLinearMemory(capacity uint64) (*cowLinearMemory, error) {
	if capacity == 0 || capacity > uint64(math.MaxInt) {
		return nil, fmt.Errorf("%w: invalid capacity %d", ErrCowUnavailable, capacity)
	}
	region, err := unix.Mmap(-1, 0, int(capacity), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANON)
	if err != nil {
		return nil, fmt.Errorf("%w: mmap %d bytes: %v", ErrCowUnavailable, capacity, err)
	}
	return &cowLinearMemory{region: region, capacity: capacity}, nil
}

// Reallocate implements experimental.LinearMemory. Growth is allowed while the
// module is being prepared. Once attached, any size change fails closed.
func (m *cowLinearMemory) Reallocate(size uint64) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.freed || size > m.capacity {
		return nil
	}
	if m.image != nil && size != m.length {
		return nil
	}
	if size > m.length {
		clear(m.region[m.length:size])
	}
	m.length = size
	return m.visibleLocked()
}

// Free implements experimental.LinearMemory. Wazero calls this during module
// close, so it is the sole owner of the per-instance munmap.
func (m *cowLinearMemory) Free() {
	m.mu.Lock()
	if m.freed {
		m.mu.Unlock()
		return
	}
	region := m.region
	m.region = nil
	m.length = 0
	m.capacity = 0
	m.image = nil
	m.freed = true
	m.mu.Unlock()
	if len(region) != 0 {
		_ = unix.Munmap(region)
	}
}

func (m *cowLinearMemory) Bytes() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.visibleLocked()
}

func (m *cowLinearMemory) visibleLocked() []byte {
	if m.freed || m.length == 0 {
		return nil
	}
	return m.region[:m.length:m.length]
}

func (m *cowLinearMemory) Attached() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.freed && m.image != nil
}

func (m *cowLinearMemory) ImageID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.image.ID()
}

func (m *cowLinearMemory) attach(image *cowImage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.freed {
		return ErrCowMemoryFreed
	}
	if image == nil || image.closed.Load() {
		return ErrCowImageClosed
	}
	if m.image != nil {
		if m.image == image {
			return nil
		}
		return fmt.Errorf("%w: memory is already attached to another image", ErrCowMemoryDrifted)
	}
	if m.length != image.size {
		return fmt.Errorf("%w: image=%d memory=%d", ErrCowBaselineMismatch, image.size, m.length)
	}
	if err := remapCowImage(m.region, m.length, image.fd); err != nil {
		return err
	}
	m.image = image
	return nil
}

// Reset replaces the visible mapping with a fresh MAP_PRIVATE view of the
// canonical image. This discards every private COW page without a Host copy.
func (m *cowLinearMemory) Reset() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.freed {
		return ErrCowMemoryFreed
	}
	if m.image == nil {
		return ErrCowNotAttached
	}
	if m.image.closed.Load() || m.image.fd < 0 {
		return ErrCowImageClosed
	}
	if m.length != m.image.size || len(m.region) < int(m.length) {
		return fmt.Errorf("%w: image=%d memory=%d", ErrCowMemoryDrifted, m.image.size, m.length)
	}
	return remapCowImage(m.region, m.length, m.image.fd)
}

func remapCowImage(region []byte, size uint64, fd int) error {
	if size == 0 || len(region) < int(size) || fd < 0 {
		return fmt.Errorf("%w: invalid remap range size=%d region=%d fd=%d", ErrCowMemoryDrifted, size, len(region), fd)
	}
	base := uintptr(unsafe.Pointer(unsafe.SliceData(region)))
	mapped, _, errno := syscall.RawSyscall6(
		syscall.SYS_MMAP,
		base,
		uintptr(size),
		uintptr(unix.PROT_READ|unix.PROT_WRITE),
		uintptr(unix.MAP_PRIVATE|unix.MAP_FIXED),
		uintptr(fd),
		0,
	)
	runtime.KeepAlive(region)
	if errno != 0 {
		return fmt.Errorf("cow: MAP_PRIVATE remap: %w", errno)
	}
	if mapped != base {
		return fmt.Errorf("%w: requested=%#x mapped=%#x", ErrCowMemoryDrifted, base, mapped)
	}
	return nil
}

//go:build !linux

package wasm

// NewSnapshotStrategy returns FullMemcpyStrategy on non-Linux platforms where
// userfaultfd is not available.
func NewSnapshotStrategy() SnapshotStrategy {
	return NewFullMemcpyStrategy()
}

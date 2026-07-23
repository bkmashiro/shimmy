//go:build !linux

package wasm

import (
	"context"

	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// Non-Linux builds retain the explicit COW configuration surface but fall back
// to the always-available full-copy strategy. Canonical COW evidence is Linux-only.
type cowImageCoordinator struct{}

func newCowImageCoordinator() *cowImageCoordinator { return &cowImageCoordinator{} }
func (c *cowImageCoordinator) ImageID() string     { return "" }
func (c *cowImageCoordinator) Close() error        { return nil }

type cowRuntimeSupport struct{}

func newCowRuntimeSupport(_ string, _ *cowImageCoordinator) *cowRuntimeSupport { return nil }
func (s *cowRuntimeSupport) instantiateContext(ctx context.Context) context.Context {
	return ctx
}
func (s *cowRuntimeSupport) snapshotStrategy(_ api.Memory, _ *zap.Logger) SnapshotStrategy {
	return NewFullMemcpyStrategy()
}

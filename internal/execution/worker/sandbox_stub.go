//go:build !linux

package worker

import (
	"context"
	"errors"

	"go.uber.org/zap"
)

// NewSandboxedWorkerFactory is not supported on non-Linux platforms.
func NewSandboxedWorkerFactory(_ SandboxConfig) (func(context.Context, StartConfig, *zap.Logger) (Worker, error), error) {
	return nil, errors.New("nsjail sandboxing is only supported on Linux")
}

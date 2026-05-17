//go:build !linux

package wasm

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// PythonDispatcher is not supported on non-Linux platforms.
type PythonDispatcher struct{}

func NewPythonDispatcher(cfg Config, log *zap.Logger) *PythonDispatcher {
	return &PythonDispatcher{}
}

func (d *PythonDispatcher) Start(ctx context.Context) error {
	return fmt.Errorf("python-wasm backend is only supported on Linux")
}

func (d *PythonDispatcher) Send(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	return nil, fmt.Errorf("python-wasm backend is only supported on Linux")
}

func (d *PythonDispatcher) Shutdown(ctx context.Context) error {
	return nil
}

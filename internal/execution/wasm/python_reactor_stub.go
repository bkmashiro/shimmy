//go:build !linux

package wasm

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// ReactorPythonDispatcher is not supported on non-Linux platforms.
type ReactorPythonDispatcher struct{}

func NewReactorPythonDispatcher(cfg Config, log *zap.Logger) *ReactorPythonDispatcher {
	return &ReactorPythonDispatcher{}
}

func (d *ReactorPythonDispatcher) Start(ctx context.Context) error {
	return fmt.Errorf("reactor-python backend is only supported on Linux")
}

func (d *ReactorPythonDispatcher) Send(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	return nil, fmt.Errorf("reactor-python backend is only supported on Linux")
}

func (d *ReactorPythonDispatcher) Shutdown(ctx context.Context) error {
	return nil
}

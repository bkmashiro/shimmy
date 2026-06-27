package wasm

import (
	"context"
	"fmt"

	"github.com/lambda-feedback/shimmy/internal/execution/dispatcher"
	"go.uber.org/zap"
)

// ReactorPythonDispatcher is the placeholder for the python-reactor WASM
// profile. The first migration slice wires profile selection and validates the
// operator contract; the heavy CPython-WASI runner implementation is migrated in
// a follow-up without committing a large python-reactor.wasm artifact.
type ReactorPythonDispatcher struct {
	cfg Config
	log *zap.Logger
}

var _ dispatcher.Dispatcher = (*ReactorPythonDispatcher)(nil)

func NewReactorPythonDispatcher(cfg Config, log *zap.Logger) *ReactorPythonDispatcher {
	cfg.applyEnv()
	cfg.applyDefaults()
	return &ReactorPythonDispatcher{
		cfg: cfg,
		log: log.Named("dispatcher_reactor_python"),
	}
}

func (d *ReactorPythonDispatcher) Start(ctx context.Context) error {
	return fmt.Errorf("reactor-python: runner migration is not implemented in this slice")
}

func (d *ReactorPythonDispatcher) Send(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	return nil, fmt.Errorf("reactor-python: dispatcher is not started")
}

func (d *ReactorPythonDispatcher) Shutdown(ctx context.Context) error {
	return nil
}

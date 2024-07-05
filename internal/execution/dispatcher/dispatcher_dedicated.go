package dispatcher

import (
	"context"
	"fmt"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
	"go.uber.org/zap"
)

type DedicatedDispatcher struct {
	supervisor supervisor.Supervisor
	log        *zap.Logger
}

var _ Dispatcher = (*DedicatedDispatcher)(nil)

type DedicatedDispatcherConfig struct {
	// SupervisorConfig is the configuration to use for the supervisor
	Supervisor supervisor.Config `conf:"supervisor,squash"`
}

type DedicatedDispatcherParams struct {
	// Config is the config for the dispatcher and the underlying supervisors
	Config DedicatedDispatcherConfig

	// SupervisorFactory is the factory function to create a new supervisor
	SupervisorFactory SupervisorFactory

	// Log is the logger to use for the dispatcher
	Log *zap.Logger
}

func NewDedicatedDispatcher(
	params DedicatedDispatcherParams,
) (Dispatcher, error) {
	if params.SupervisorFactory == nil {
		params.SupervisorFactory = defaultSupervisorFactory
	}

	supervisor, err := createSupervisor(params)
	if err != nil {
		return nil, err
	}

	return &DedicatedDispatcher{
		supervisor: supervisor,
		log:        params.Log.Named("dispatcher_dedicated"),
	}, nil
}

func (m *DedicatedDispatcher) Start(ctx context.Context) error {
	if err := m.supervisor.Start(ctx); err != nil {
		m.log.Error("error booting", zap.Error(err))
		return err
	}

	return nil
}

func (m *DedicatedDispatcher) Send(
	ctx context.Context,
	method string,
	data map[string]any,
) (map[string]any, error) {
	res, err := m.supervisor.Send(ctx, method, data)
	if err != nil {
		m.log.Error("error sending message", zap.Error(err))
		return nil, fmt.Errorf("error sending data: %w", err)
	}

	// TODO: ignore release error?
	// TODO: move into background goroutine?
	if err := res.Release(ctx); err != nil {
		m.log.Error("error releasing worker", zap.Error(err))
		return nil, fmt.Errorf("error releasing worker: %w", err)
	}

	return res.Data, nil
}

// Shutdown stops the dispatcher and waits for all workers to finish.
func (m *DedicatedDispatcher) Shutdown(ctx context.Context) error {
	m.log.Debug("shutting down")

	wait, err := m.supervisor.Shutdown(ctx)
	if err != nil {
		m.log.Error("error shutting down", zap.Error(err))
		return err
	}

	if wait == nil {
		m.log.Warn("missing wait function")
		return nil
	}

	if err := wait(); err != nil {
		m.log.Error("error waiting for shut down", zap.Error(err))
		return err
	}

	return nil
}

func createSupervisor(
	params DedicatedDispatcherParams,
) (supervisor.Supervisor, error) {
	return params.SupervisorFactory(supervisor.Params{
		Config: params.Config.Supervisor,
		Log:    params.Log,
	})
}

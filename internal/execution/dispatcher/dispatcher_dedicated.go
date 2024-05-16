package dispatcher

import (
	"context"
	"fmt"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
	"go.uber.org/zap"
)

type DedicatedDispatcher[I, O any] struct {
	supervisor supervisor.Supervisor[I, O]
	log        *zap.Logger
}

var _ Dispatcher[any, any] = (*DedicatedDispatcher[any, any])(nil)

type DedicatedDispatcherConfig[I, O any] struct {
	// SupervisorConfig is the configuration to use for the supervisor
	Supervisor supervisor.Config[I, O] `conf:"supervisor,squash"`
}

type DedicatedDispatcherParams[I, O any] struct {
	// Config is the config for the dispatcher and the underlying supervisors
	Config DedicatedDispatcherConfig[I, O]

	// SupervisorFactory is the factory function to create a new supervisor
	SupervisorFactory SupervisorFactory[I, O]

	// Log is the logger to use for the dispatcher
	Log *zap.Logger
}

func NewDedicatedDispatcher[I, O any](params DedicatedDispatcherParams[I, O]) (Dispatcher[I, O], error) {
	log := params.Log.Named("dispatcher")

	if params.SupervisorFactory == nil {
		params.SupervisorFactory = defaultSupervisorFactory
	}

	supervisor, err := createSupervisor[I, O](params)
	if err != nil {
		return nil, err
	}

	return &DedicatedDispatcher[I, O]{
		supervisor: supervisor,
		log:        log.Named("controller"),
	}, nil
}

func (m *DedicatedDispatcher[I, O]) Start(ctx context.Context) error {
	m.log.Debug("booting supervisor")

	if err := m.supervisor.Start(ctx); err != nil {
		m.log.Error("error booting supervisor", zap.Error(err))
		return err
	}

	m.log.Debug("done booting supervisor")

	return nil
}

func (m *DedicatedDispatcher[I, O]) Send(ctx context.Context, data I) (O, error) {

	m.log.Debug("sending message")

	res, err := m.supervisor.Send(ctx, data)
	if err != nil {
		m.log.Error("error sending data to supervisor", zap.Error(err))
		var zero O
		return zero, fmt.Errorf("error sending data: %w", err)
	}

	// TODO: ignore release error?
	if err := res.Release(); err != nil {
		m.log.Error("error releasing worker", zap.Error(err))
		return res.Data, fmt.Errorf("error releasing worker: %w", err)
	}

	m.log.Debug("message sent to supervisor")

	return res.Data, nil
}

// Shutdown stops the dispatcher and waits for all workers to finish.
func (m *DedicatedDispatcher[I, O]) Shutdown(ctx context.Context) error {
	m.log.Debug("shutting down supervisor")

	wait, err := m.supervisor.Shutdown(ctx)
	if err != nil {
		m.log.Error("error shutting down supervisor", zap.Error(err))
		return err
	}

	if wait == nil {
		m.log.Warn("supervisor did not return a wait function")
		return nil
	}

	if err := wait(); err != nil {
		m.log.Error("error waiting for supervisor to shut down", zap.Error(err))
		return err
	}

	m.log.Debug("supervisor shut down")

	return nil
}

func createSupervisor[I, O any](params DedicatedDispatcherParams[I, O]) (supervisor.Supervisor[I, O], error) {
	return params.SupervisorFactory(supervisor.Params[I, O]{
		Config: params.Config.Supervisor,
		Log:    params.Log,
	})
}

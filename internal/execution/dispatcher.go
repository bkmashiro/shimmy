package execution

import (
	"context"

	"github.com/lambda-feedback/shimmy/internal/execution/dispatcher"
	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
	"go.uber.org/zap"
)

type Dispatcher[I, O any] dispatcher.Dispatcher[I, O]

type Config[I, O any] struct {
	// MaxWorkers is the maximum number of concurrent workers
	// when employing a pooled dispatcher.
	MaxWorkers int `conf:"max_workers"`

	// SupervisorConfig is the configuration to use for the supervisor
	Supervisor supervisor.Config[I, O] `conf:",squash"`
}

type Params[I, O any] struct {
	// Context is the context to use for the dispatcher
	Context context.Context

	// Config is the config for the dispatcher and the underlying supervisors
	Config Config[I, O]

	// Log is the logger to use for the dispatcher
	Log *zap.Logger
}

func NewDispatcher[I, O any](
	params Params[I, O],
) (dispatcher.Dispatcher[I, O], error) {
	if params.Config.Supervisor.Persistent {
		return dispatcher.NewDedicatedDispatcher(
			dispatcher.DedicatedDispatcherParams[I, O]{
				Config: dispatcher.DedicatedDispatcherConfig[I, O]{
					Supervisor: params.Config.Supervisor,
				},
				Log: params.Log,
			},
		)
	} else {
		return dispatcher.NewPooledDispatcher(
			dispatcher.PooledDispatcherParams[I, O]{
				Config: dispatcher.PooledDispatcherConfig[I, O]{
					Supervisor: params.Config.Supervisor,
					MaxWorkers: params.Config.MaxWorkers,
				},
				Context: params.Context,
				Log:     params.Log,
			},
		)
	}
}

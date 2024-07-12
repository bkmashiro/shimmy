package runtime

import (
	"context"

	"github.com/lambda-feedback/shimmy/internal/execution"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Runtime is the interface for a runtime.
type Runtime interface {
	Handle(context.Context, EvaluationRequest) (EvaluationResponse, error)

	Start(context.Context) error

	Shutdown(context.Context) error
}

// Params is the runtime-specific params type.
type Params = execution.Params

// Dispatcher is the runtime-specific dispatcher type.
type Dispatcher = execution.Dispatcher

// Config is the runtime-specific type for the config.
type Config = execution.Config

// EvaluationRuntime is a runtime that uses the execution manager.
type EvaluationRuntime struct {
	dispatcher Dispatcher

	log *zap.Logger
}

var _ Runtime = (*EvaluationRuntime)(nil)

// RuntimeParams defines the dependencies for the runtime.
type RuntimeParams struct {
	fx.In

	// Config is the conext to use for the underlying runtime
	Context context.Context

	// Config is the config for the underlying runtime manager
	Config Config

	// Log is the logger to use for the runtime
	Log *zap.Logger
}

// NewRuntime creates a new runtime.
func NewRuntime(params RuntimeParams) (Runtime, error) {
	dispatcher, err := execution.NewDispatcher(Params{
		Context: params.Context,
		Config:  params.Config,
		Log:     params.Log,
	})
	if err != nil {
		return nil, err
	}

	return &EvaluationRuntime{
		dispatcher: dispatcher,
		log:        params.Log.Named("runtime"),
	}, nil
}

func NewLifecycleRuntime(params RuntimeParams, lc fx.Lifecycle) (Runtime, error) {
	r, err := NewRuntime(params)
	if err != nil {
		return nil, err
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return r.Start(ctx)
		},
		OnStop: func(ctx context.Context) error {
			return r.Shutdown(ctx)
		},
	})

	return r, nil
}

func (r *EvaluationRuntime) Start(ctx context.Context) error {
	return r.dispatcher.Start(ctx)
}

func (r *EvaluationRuntime) Handle(
	ctx context.Context,
	message EvaluationRequest,
) (EvaluationResponse, error) {
	return r.dispatcher.Send(ctx, string(message.Command), message.Data)
}

func (r *EvaluationRuntime) Shutdown(ctx context.Context) error {
	return r.dispatcher.Shutdown(ctx)
}

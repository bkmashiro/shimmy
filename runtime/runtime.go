package runtime

import (
	"context"

	"github.com/lambda-feedback/shimmy/internal/execution"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Runtime is the interface for a runtime.
type Runtime interface {
	Handle(context.Context, Message) (Message, error)
}

// ManagerParams is the runtime-specific params type for the manager.
type ManagerParams = execution.Params[Message, Message]

// Manager is the runtime-specific type for the manager.
type Manager = execution.Manager[Message, Message]

// Config is the runtime-specific type for the config.
type Config = execution.Config[Message, Message]

// EvaluationRuntime is a runtime that uses the execution manager.
type EvaluationRuntime struct {
	manager Manager

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
	manager, err := execution.NewManager(ManagerParams{
		Context: params.Context,
		Config:  params.Config,
		Log:     params.Log,
	})
	if err != nil {
		return nil, err
	}

	return &EvaluationRuntime{
		manager: manager,
		log:     params.Log,
	}, nil
}

func (r *EvaluationRuntime) Handle(
	ctx context.Context,
	message Message,
) (Message, error) {
	return r.manager.Send(ctx, message)
}

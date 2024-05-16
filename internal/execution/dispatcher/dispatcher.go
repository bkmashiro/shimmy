package dispatcher

import (
	"context"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

type Dispatcher[I, O any] interface {
	// Send sends data to a supervisor and returns the result
	Send(context.Context, I) (O, error)

	// Start starts the dispatcher and all workers
	Start(context.Context) error

	// Shutdown stops the dispatcher and waits for all workers to finish.
	Shutdown(context.Context) error
}

type SupervisorFactory[I, O any] func(supervisor.Params[I, O]) (supervisor.Supervisor[I, O], error)

func defaultSupervisorFactory[I, O any](
	params supervisor.Params[I, O],
) (supervisor.Supervisor[I, O], error) {
	return supervisor.New(params)
}

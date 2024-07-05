package dispatcher

import (
	"context"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

type Dispatcher interface {
	// Send sends data to a supervisor and returns the result
	Send(context.Context, string, map[string]any) (map[string]any, error)

	// Start starts the dispatcher and all workers
	Start(context.Context) error

	// Shutdown stops the dispatcher and waits for all workers to finish.
	Shutdown(context.Context) error
}

type SupervisorFactory func(supervisor.Params) (supervisor.Supervisor, error)

func defaultSupervisorFactory(
	params supervisor.Params,
) (supervisor.Supervisor, error) {
	return supervisor.New(params)
}

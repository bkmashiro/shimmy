package supervisor

import (
	"context"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"go.uber.org/zap"
)

// AdapterFactoryFn is a type alias for a function that creates an adapter
// based on the given IO mode.
type AdapterFactoryFn[I, O any] func(worker.Worker, IOInterface, *zap.Logger) (Adapter[I, O], error)

// WaitFunc is a function that can be used to wait for a resource to be released.
type WaitFunc func() error

// ReleaseFunc is a function that can be used to release resources.
type ReleaseFunc func(context.Context) error

// noopReleaseFunc is a no-op release function that always returns nil.
var noopReleaseFunc = func(context.Context) error { return nil }

type Adapter[I, O any] interface {
	// Start allows to start the worker with the given configuration.
	// The worker is expected to be started in a non-blocking manner.
	Start(context.Context, worker.StartConfig) error

	// Stop stops the worker with the given configuration. The worker is
	// expected to be stopped in a non-blocking manner. The returned
	// ReleaseFunc can be used to wait for the worker to terminate.
	Stop(worker.StopConfig) (ReleaseFunc, error)

	// Send sends the given data to the worker and returns the response.
	Send(context.Context, I, time.Duration) (O, error)
}

// MARK: - factory

// defaultAdapterFactory is the default adapter factory
// that creates an adapter based on the given IO mode.
func defaultAdapterFactory[I, O any](
	worker worker.Worker,
	mode IOInterface,
	log *zap.Logger,
) (Adapter[I, O], error) {
	switch mode {
	case FileIO:
		return newFileAdapter[I, O](worker, log), nil
	case StdIO:
		return newStdioAdapter[I, O](worker, log), nil
	// case SocketIO:
	// 	return &socketAdapter[I, O]{log: log}, nil
	default:
		return nil, ErrUnsupportedIOMode
	}
}

// MARK: - helpers

// stopWorker is a helper function to stop a worker and return a wait
// function that can be used to wait for the worker to terminate.
func stopWorker(w worker.Worker, params worker.StopConfig) (ReleaseFunc, error) {

	// TODO: what if shutdown fails? we have a zombie worker then...

	// gracefully shutdown the worker
	if err := w.Terminate(); err != nil {
		// no need to wait for termination if we could not terminate
		return nil, err
	}

	releaseFunc := func(ctx context.Context) error {
		// wait for the worker to terminate
		_, err := w.WaitFor(ctx, params.Timeout)
		if err != nil {
			return err
		}

		return nil
	}

	return releaseFunc, nil
}

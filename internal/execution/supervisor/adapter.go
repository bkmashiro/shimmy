package supervisor

import (
	"context"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"go.uber.org/zap"
)

// AdapterFactoryFn is a type alias for a function that creates an adapter
// based on the given IO mode.
type AdapterFactoryFn[I, O any] func(IOInterface, *zap.Logger) (Adapter[I, O], error)

// WaitFunc is a function that can be used to wait for a worker to terminate.
type WaitFunc func() error

type Adapter[I, O any] interface {
	// Start allows to start the worker with the given configuration.
	// The worker is expected to be started in a non-blocking manner.
	Start(context.Context, worker.StartConfig) error

	// Stop stops the worker with the given configuration. The worker is
	// expected to be stopped in a non-blocking manner. The returned
	// WaitFunc can be used to wait for the worker to terminate.
	Stop(context.Context, worker.StopConfig) (WaitFunc, error)

	// Send sends the given data to the worker and returns the response.
	Send(context.Context, I, worker.SendConfig) (O, error)
}

// MARK: - factory

// defaultAdapterFactory is the default adapter factory that creates an adapter
// based on the given IO mode.
func defaultAdapterFactory[I, O any](
	mode IOInterface,
	log *zap.Logger,
) (Adapter[I, O], error) {
	switch mode {
	case FileIO:
		return newFileAdapter[I, O](log), nil
	case StdIO:
		return newStdioAdapter[I, O](log), nil
	// case SocketIO:
	// 	return &socketAdapter[I, O]{log: log}, nil
	default:
		return nil, ErrUnsupportedIOMode
	}
}

// MARK: - helpers

// stopWorker is a helper function to stop a worker and return a wait function
// that can be used to wait for the worker to terminate.
func stopWorker[I, O any](
	ctx context.Context,
	w worker.Worker[I, O],
	params worker.StopConfig,
) (WaitFunc, error) {

	// TODO: what if shutdown fails? we have a zombie worker then...

	// gracefully shutdown the worker
	if err := w.Terminate(); err != nil {
		// no need to wait for termination if we could not terminate
		return nil, err
	}

	waitFunc := func() error {
		// wait for the worker to terminate
		_, err := w.WaitFor(ctx, params.Timeout)
		if err != nil {
			return err
		}

		return nil
	}

	return waitFunc, nil
}

package supervisor

import (
	"context"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"go.uber.org/zap"
)

// AdapterWorkerFactoryFn is a type alias for a function that creates a worker
// based on the given context and configuration.
type AdapterWorkerFactoryFn func(worker.StartConfig) (worker.Worker, error)

// AdapterFactoryFn is a type alias for a function that creates an adapter
// based on the given IO mode.
type AdapterFactoryFn func(AdapterWorkerFactoryFn, IOConfig, *zap.Logger) (Adapter, error)

// WaitFunc is a function that can be used to wait for a resource to be released.
type WaitFunc func() error

// ReleaseFunc is a function that can be used to release resources.
type ReleaseFunc func(context.Context) error

// noopReleaseFunc is a no-op release function that always returns nil.
var noopReleaseFunc = func(context.Context) error { return nil }

type Adapter interface {
	// Start allows to start the worker with the given configuration.
	// The worker is expected to be started in a non-blocking manner.
	Start(context.Context, worker.StartConfig) error

	// Stop stops the worker with the given configuration. The worker is
	// expected to be stopped in a non-blocking manner. The returned
	// ReleaseFunc can be used to wait for the worker to terminate.
	Stop() (ReleaseFunc, error)

	// Send sends the given data to the worker and returns the response.
	Send(context.Context, string, map[string]any, time.Duration) (map[string]any, error)
}

// MARK: - factory

// defaultAdapterFactory is the default adapter factory
// that creates an adapter based on the given IO mode.
func defaultAdapterFactory(
	workerFactory AdapterWorkerFactoryFn,
	config IOConfig,
	log *zap.Logger,
) (Adapter, error) {
	switch config.Interface {
	case FileIO:
		return newFileAdapter(workerFactory, log), nil
	case RpcIO:
		return newRpcAdapter(workerFactory, config.Rpc, log), nil
	default:
		return nil, ErrUnsupportedIOInterface
	}
}

// MARK: - helpers

// stopWorker is a helper function to stop a worker and return a wait
// function that can be used to wait for the worker to terminate.
func stopWorker(w worker.Worker) (ReleaseFunc, error) {

	// TODO: what if shutdown fails? we have a zombie worker then...

	// gracefully shutdown the worker
	if err := w.Stop(); err != nil {
		// no need to wait for termination if we could not terminate
		return nil, err
	}

	releaseFunc := func(ctx context.Context) error {
		// wait for the worker to terminate
		_, err := w.Wait(ctx)
		if err != nil {
			return err
		}

		return nil
	}

	return releaseFunc, nil
}

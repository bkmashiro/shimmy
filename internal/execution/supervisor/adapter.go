package supervisor

import (
	"context"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"go.uber.org/zap"
)

type AdapterFactoryFn[I, O any] func(IOMode, *zap.Logger) (Adapter[I, O], error)

type WaitFunc func() error

type Adapter[I, O any] interface {
	Start(context.Context, worker.StartConfig) error
	Stop(context.Context, worker.StopConfig) (WaitFunc, error)
	Send(context.Context, I, worker.SendConfig) (O, error)
}

// MARK: - factory

func defaultAdapterFactory[I, O any](
	mode IOMode,
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

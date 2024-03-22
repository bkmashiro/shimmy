package supervisor

import (
	"context"

	"github.com/lambda-feedback/shimmy/worker"
	"go.uber.org/zap"
)

type AdapterFactoryFn[I, O any] func(IOMode, *zap.Logger) (Adapter[I, O], error)

type WaitFunc func() error

type Adapter[I, O any] interface {
	Start(context.Context, worker.StartParams) error
	Stop(context.Context, worker.StopParams) (WaitFunc, error)
	Send(context.Context, I, worker.SendParams) (O, error)
}

// MARK: - factory

func defaultAdapterFactory[I, O any](
	mode IOMode,
	log *zap.Logger,
) (Adapter[I, O], error) {
	switch mode {
	case FileIO:
		return &fileAdapter[I, O]{log: log}, nil
	case StdIO:
		return &stdioAdapter[I, O]{log: log}, nil
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
	params worker.StopParams,
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

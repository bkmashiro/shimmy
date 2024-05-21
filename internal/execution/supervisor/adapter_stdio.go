package supervisor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"go.uber.org/zap"
)

type stdioAdapter[I, O any] struct {
	workerFactory AdapterWorkerFactoryFn

	worker worker.Worker

	log *zap.Logger
}

func newStdioAdapter[I, O any](
	workerFactory AdapterWorkerFactoryFn,
	log *zap.Logger,
) *stdioAdapter[I, O] {
	return &stdioAdapter[I, O]{
		workerFactory: workerFactory,
		log:           log.Named("adapter_stdio"),
	}
}

func (a *stdioAdapter[I, O]) Start(
	ctx context.Context,
	params worker.StartConfig,
) error {
	if a.workerFactory == nil {
		return errors.New("no worker factory provided")
	}

	// create the worker
	worker, err := a.workerFactory(ctx, params)
	if err != nil {
		return fmt.Errorf("error creating worker: %w", err)
	}

	a.worker = worker

	// for stdio, we can already start the worker, as we do not need to pass
	// any additional, message-specific data to the worker via arguments
	if err := worker.Start(ctx); err != nil {
		return fmt.Errorf("error starting worker: %w", err)
	}

	return nil
}

func (a *stdioAdapter[I, O]) Send(
	ctx context.Context,
	data I,
	timeout time.Duration,
) (O, error) {
	var res O

	if a.worker == nil {
		return res, errors.New("no worker provided")
	}

	// TODO: send data to worker
	// res, err := a.worker.Send(ctx, data, timeout)
	// if err != nil {
	// 	a.log.Debug("error sending data to worker", zap.Error(err))
	// 	return res, err
	// }

	return res, nil
}

func (a *stdioAdapter[I, O]) Stop(
	params worker.StopConfig,
) (ReleaseFunc, error) {
	if a.worker == nil {
		return nil, errors.New("no worker provided")
	}

	return stopWorker(a.worker, params)
}

package supervisor

import (
	"context"
	"errors"

	"github.com/lambda-feedback/shimmy/worker"
	"go.uber.org/zap"
)

type stdioAdapter[I, O any] struct {
	worker worker.Worker[I, O]

	log *zap.Logger
}

func newStdioAdapter[I, O any](log *zap.Logger) *stdioAdapter[I, O] {
	worker := worker.NewProcessWorker[I, O](log)

	return &stdioAdapter[I, O]{
		worker: worker,
		log:    log,
	}
}

func (a *stdioAdapter[I, O]) Start(ctx context.Context, params worker.StartParams) error {
	if a.worker == nil {
		return errors.New("no worker provided")
	}

	// for stdio, we can already start the worker, as we do not need to pass
	// any additional, message-specific data to the worker via arguments
	if err := a.worker.Start(ctx, params); err != nil {
		a.log.Error("error starting worker", zap.Error(err))
		return err
	}

	return nil
}

func (a *stdioAdapter[I, O]) Send(
	ctx context.Context,
	data I,
	params worker.SendParams,
) (O, error) {
	var res O

	if a.worker == nil {
		return res, errors.New("no worker provided")
	}

	// send data to worker
	res, err := a.worker.Send(ctx, data, params)
	if err != nil {
		a.log.Error("error sending data to worker", zap.Error(err))
		return res, err
	}

	return res, nil
}

func (a *stdioAdapter[I, O]) Stop(
	ctx context.Context,
	params worker.StopParams,
) (WaitFunc, error) {
	if a.worker == nil {
		return nil, errors.New("no worker provided")
	}

	return stopWorker(ctx, a.worker, params)
}

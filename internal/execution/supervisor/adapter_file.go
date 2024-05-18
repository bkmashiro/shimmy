package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"go.uber.org/zap"
)

// fileAdapter is an adapter that allows supervisors to use files to communicate
// with their worker. This is useful when the worker process is not able to
// communicate with the supervisor process via stdio or sockets.
type fileAdapter[I, O any] struct {
	// worker is the worker that is managed by the adapter.
	worker worker.Worker[any, any]

	// startParams is the start configuration that is used to start the worker.
	// The file adapter does not start the worker during
	startParams worker.StartConfig

	log *zap.Logger
}

var _ Adapter[any, any] = (*fileAdapter[any, any])(nil)

func newFileAdapter[I, O any](log *zap.Logger) *fileAdapter[I, O] {
	worker := worker.NewProcessWorker[any, any](log)

	return &fileAdapter[I, O]{
		worker: worker,
		log:    log.Named("adapter_file"),
	}
}

func (a *fileAdapter[I, O]) Start(ctx context.Context, params worker.StartConfig) error {
	// for fileio, we can't yet start the worker, as we do need to pass
	// the file path with the request data to the worker via arguments.

	// however, we do store the start params and use them in Send later.
	a.startParams = params

	return nil
}

func (a *fileAdapter[I, O]) Send(
	ctx context.Context,
	data I,
	params worker.SendConfig,
) (O, error) {
	var out O

	if a.worker == nil {
		return out, errors.New("no worker provided")
	}

	// create temp files for request and response data
	reqFile, err := os.CreateTemp("", "request-data-*")
	if err != nil {
		a.log.Debug("error creating temp file", zap.Error(err))
		return out, err
	}
	// defer os.Remove(reqFile.Name())

	resFile, err := os.CreateTemp("", "response-data-*")
	if err != nil {
		a.log.Debug("error creating temp req file", zap.Error(err))
		return out, err
	}
	defer resFile.Close()
	// defer os.Remove(resFile.Name())

	// write data to request file
	if err := json.NewEncoder(reqFile).Encode(data); err != nil {
		a.log.Debug("error writing temp req file", zap.Error(err))
		return out, err
	}

	// close & flush request file
	if err := reqFile.Close(); err != nil {
		a.log.Debug("error closing temp req file", zap.Error(err))
		return out, err
	}

	startParams := a.startParams

	// append req and res file names to worker arguments
	startParams.Args = append(startParams.Args, reqFile.Name(), resFile.Name())

	// ensure env is not nil
	if startParams.Env == nil {
		startParams.Env = make([]string, 0, 2)
	}

	// append req and res file names to worker env
	startParams.Env = append(startParams.Env,
		"REQUEST_FILE_NAME="+reqFile.Name(),
		"RESPONSE_FILE_NAME="+resFile.Name(),
	)

	a.log.Debug("starting worker")

	// start worker with modified args and env
	if err := a.worker.Start(ctx, startParams); err != nil {
		a.log.Debug("error starting worker", zap.Error(err))
		return out, err
	}

	// wait for worker to terminate (maybe find another way to read res earlier?)
	// TODO: investigate use of status returned by `WaitFor`
	exitEvent, err := a.worker.WaitFor(ctx, params.Timeout)
	if err != nil {
		a.log.Debug("error waiting for worker to finish", zap.Error(err))
		return out, err
	}

	a.log.Debug("worker finished", zap.Any("exit", exitEvent))

	var res O

	// read and decode response data from res file
	if err := json.NewDecoder(resFile).Decode(&res); err != nil {
		a.log.Debug("error decoding response data from temp file", zap.Error(err))
		return out, err
	}

	return res, nil
}

func (a *fileAdapter[I, O]) Stop(
	ctx context.Context,
	params worker.StopConfig,
) (WaitFunc, error) {
	if a.worker == nil {
		return nil, errors.New("no worker provided")
	}

	a.log.Debug("stopping worker", zap.Any("params", params))

	return stopWorker(ctx, a.worker, params)
}

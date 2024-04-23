package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"go.uber.org/zap"
)

type fileAdapter[I, O any] struct {
	worker worker.Worker[any, any]

	startParams worker.StartConfig

	log *zap.Logger
}

func newFileAdapter[I, O any](log *zap.Logger) *fileAdapter[I, O] {
	worker := worker.NewProcessWorker[any, any](log)

	return &fileAdapter[I, O]{
		worker: worker,
		log:    log,
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
		a.log.Error("error creating temp file", zap.Error(err))
		return out, err
	}
	defer reqFile.Close()
	defer os.Remove(reqFile.Name())

	resFile, err := os.CreateTemp("", "response-data-*")
	if err != nil {
		a.log.Error("error creating temp req file", zap.Error(err))
		return out, err
	}
	defer resFile.Close()
	defer os.Remove(resFile.Name())

	req := worker.Message[I]{
		Data: data,
	}

	// write data to request file
	if err := json.NewEncoder(reqFile).Encode(req); err != nil {
		a.log.Error("error writing temp req file", zap.Error(err))
		return out, err
	}

	startParams := a.startParams

	// append req and res file names to worker arguments
	startParams.Args = append(startParams.Args, reqFile.Name(), resFile.Name())

	if startParams.Env == nil {
		startParams.Env = make(map[string]string)
	}

	// append req and res file names to worker env
	startParams.Env["REQUEST_FILE_NAME"] = reqFile.Name()
	startParams.Env["RESPONSE_FILE_NAME"] = resFile.Name()

	// start worker with modified args and env
	if err := a.worker.Start(ctx, startParams); err != nil {
		a.log.Error("error starting worker", zap.Error(err))
		return out, err
	}

	// wait for worker to terminate (maybe find another way to read res earlier?)
	// TODO: investigate use of status returned by `WaitFor`
	_, err = a.worker.WaitFor(ctx, params.Timeout)
	if err != nil {
		a.log.Error("error waiting for worker to finish", zap.Error(err))
		return out, err
	}

	var res worker.Message[O]

	// read response data from res file
	if err := json.NewDecoder(resFile).Decode(&res); err != nil {
		a.log.Error("error reading temp req file", zap.Error(err))
		return out, err
	}

	return res.Data, nil
}

func (a *fileAdapter[I, O]) Stop(
	ctx context.Context,
	params worker.StopConfig,
) (WaitFunc, error) {
	if a.worker == nil {
		return nil, errors.New("no worker provided")
	}

	return stopWorker(ctx, a.worker, params)
}

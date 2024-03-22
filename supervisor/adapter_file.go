package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/lambda-feedback/shimmy/worker"
	"go.uber.org/zap"
)

type fileAdapter[I, O any] struct {
	worker worker.Worker[any, any]

	startParams worker.StartParams

	log *zap.Logger
}

func (a *fileAdapter[I, O]) Start(ctx context.Context, params worker.StartParams) error {
	if a.worker != nil {
		return errors.New("worker already started")
	}

	a.worker = worker.NewProcessWorker[any, any](a.log)
	a.startParams = params

	// for fileio, we can't yet start the worker, as we do need to pass
	// the file path with the request data to the worker via arguments.

	return nil
}

func (a *fileAdapter[I, O]) Send(
	ctx context.Context,
	data I,
	params worker.SendParams,
) (O, error) {
	var res O

	if a.worker == nil {
		return res, errors.New("worker not started")
	}

	// create temp files for request and response data
	reqFile, err := os.CreateTemp("", "request-data-*")
	if err != nil {
		a.log.Error("error creating temp file", zap.Error(err))
		return res, err
	}
	defer reqFile.Close()
	defer os.Remove(reqFile.Name())

	resFile, err := os.CreateTemp("", "response-data-*")
	if err != nil {
		a.log.Error("error creating temp req file", zap.Error(err))
		return res, err
	}
	defer resFile.Close()
	defer os.Remove(resFile.Name())

	// write data to request file
	if err := json.NewEncoder(reqFile).Encode(data); err != nil {
		a.log.Error("error writing temp req file", zap.Error(err))
		return res, err
	}

	startParams := a.startParams

	// append req and res file names to worker arguments
	startParams.Args = append(startParams.Args, reqFile.Name(), resFile.Name())

	// append req and res file names to worker env
	startParams.Env["REQUEST_FILE_NAME"] = reqFile.Name()
	startParams.Env["RESPONSE_FILE_NAME"] = resFile.Name()

	// start worker with modified args and env
	if err := a.worker.Start(ctx, startParams); err != nil {
		a.log.Error("error starting worker", zap.Error(err))
		return res, err
	}

	// wait for worker to terminate (maybe find another way to read res earlier?)
	// TODO: investigate use of status returned by `WaitFor`
	_, err = a.worker.WaitFor(ctx, params.Timeout)
	if err != nil {
		a.log.Error("error waiting for worker to finish", zap.Error(err))
		return res, err
	}

	// read response data from res file
	if err := json.NewDecoder(resFile).Decode(&res); err != nil {
		a.log.Error("error reading temp req file", zap.Error(err))
		return res, err
	}

	return res, nil
}

func (a *fileAdapter[I, O]) Stop(
	ctx context.Context,
	params worker.StopParams,
) (WaitFunc, error) {
	if a.worker == nil {
		return nil, errors.New("worker not started")
	}

	return stopWorker(ctx, a.worker, params)
}

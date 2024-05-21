package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"go.uber.org/zap"
)

// fileAdapter is an adapter that allows supervisors to use files to
// communicate with their worker. This is useful when stdio or sockets
// can't be used for communication.
type fileAdapter[I, O any] struct {
	// workerFactory is the worker that is managed by the adapter.
	workerFactory AdapterWorkerFactoryFn

	// startParams is the start configuration that is used to start the worker.
	// The file adapter does not start the worker during
	startParams worker.StartConfig

	log *zap.Logger
}

var _ Adapter[any, any] = (*fileAdapter[any, any])(nil)

func newFileAdapter[I, O any](
	workerFactory AdapterWorkerFactoryFn,
	log *zap.Logger,
) *fileAdapter[I, O] {
	return &fileAdapter[I, O]{
		workerFactory: workerFactory,
		log:           log.Named("adapter_file"),
	}
}

func (a *fileAdapter[I, O]) Start(
	ctx context.Context,
	params worker.StartConfig,
) error {
	// for fileio, we can't yet start the worker, as we do need to pass
	// the file path with the request data to the worker via arguments.

	// however, we do store the start params and use them in Send later.
	a.startParams = params

	return nil
}

func (a *fileAdapter[I, O]) Send(
	ctx context.Context,
	data I,
	timeout time.Duration,
) (O, error) {
	var out O

	if a.workerFactory == nil {
		return out, errors.New("no worker factory provided")
	}

	// create temp files for request and response data
	reqFile, err := os.CreateTemp("", "request-data-*")
	if err != nil {
		return out, fmt.Errorf("error creating temp file: %w", err)
	}
	defer func() {
		if err := os.Remove(reqFile.Name()); err != nil {
			a.log.Error("failed to remove request file", zap.Error(err))
		}
	}()

	resFile, err := os.CreateTemp("", "response-data-*")
	if err != nil {
		return out, fmt.Errorf("error creating temp file: %w", err)
	}

	defer func() {
		if err := resFile.Close(); err != nil {
			a.log.Error("failed to close response file", zap.Error(err))
		}
	}()

	defer func() {
		if err := os.Remove(resFile.Name()); err != nil {
			a.log.Error("failed to remove response file", zap.Error(err))
		}
	}()

	// write data to request file
	if err := json.NewEncoder(reqFile).Encode(data); err != nil {
		return out, fmt.Errorf("error writing request data: %w", err)
	}

	// close & flush request file
	if err := reqFile.Close(); err != nil {
		return out, fmt.Errorf("error closing request file: %w", err)
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

	// create the worker with modified args and env
	worker, err := a.workerFactory(ctx, startParams)
	if err != nil {
		return out, fmt.Errorf("error creating worker: %w", err)
	}

	pipe, err := worker.ReadPipe()
	if err != nil {
		return out, fmt.Errorf("error getting read pipe: %w", err)
	}

	var stdoutWg sync.WaitGroup

	stdoutWg.Add(1)
	go func() {
		defer stdoutWg.Done()

		// capture stdout
		var buf bytes.Buffer
		_, err := io.Copy(&buf, pipe)
		if err != nil && err != io.EOF {
			a.log.Warn("failed to read from stdout",
				zap.String("data", buf.String()),
				zap.Error(err),
			)
		}
		a.log.Debug("stdout", zap.String("data", buf.String()))
	}()

	if err := worker.Start(ctx); err != nil {
		return out, fmt.Errorf("error starting process: %w", err)
	}

	stdoutWg.Wait()

	// wait for worker to terminate (find another way to read res earlier?)
	exitEvent, err := worker.WaitFor(ctx, timeout)
	if err != nil {
		return out, fmt.Errorf("error waiting for process: %w", err)
	}

	if !exitEvent.Success() {
		return out, fmt.Errorf("process exited with non-zero code: %s", exitEvent.String())
	}

	// read and decode response data from res file
	if err := json.NewDecoder(resFile).Decode(&out); err != nil {
		return out, fmt.Errorf("error decoding response data: %w", err)
	}

	return out, nil
}

func (a *fileAdapter[I, O]) Stop(
	worker.StopConfig,
) (ReleaseFunc, error) {
	// for fileio, we already stopped the worker, as we do need to wait
	// for the process to finish in order to read the response data.
	// therefore, we don't need to do anything here.

	return noopReleaseFunc, nil
}

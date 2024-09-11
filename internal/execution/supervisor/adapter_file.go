package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
)

// fileAdapter is an adapter that allows supervisors to use files to
// communicate with their worker. This is useful when stdio or sockets
// can't be used for communication.
type fileAdapter struct {
	// workerFactory is the worker that is managed by the adapter.
	workerFactory AdapterWorkerFactoryFn

	// startParams is the start configuration that is used to start the worker.
	// The file adapter does not start the worker during Start, but instead
	// uses the startParams to start the worker during Send.
	startParams worker.StartConfig

	// worker is the worker that is managed by the adapter.
	worker worker.Worker

	log *zap.Logger
}

var _ Adapter = (*fileAdapter)(nil)

func newFileAdapter(
	workerFactory AdapterWorkerFactoryFn,
	log *zap.Logger,
) *fileAdapter {
	return &fileAdapter{
		workerFactory: workerFactory,
		log:           log.Named("adapter_file"),
	}
}

func (a *fileAdapter) Start(
	ctx context.Context,
	params worker.StartConfig,
) error {
	// for fileio, we can't yet start the worker, as we do need to pass
	// the file path with the request data to the worker via arguments.

	// however, we do store the start params and use them in Send later.
	a.startParams = params

	return nil
}

func (a *fileAdapter) Send(
	ctx context.Context,
	method string,
	data map[string]any,
	timeout time.Duration,
) (map[string]any, error) {
	if a.workerFactory == nil {
		return nil, errors.New("no worker factory provided")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// temp dir path
	workingDir := path.Join(os.TempDir(), "shimmy")

	// create temp dir if it doesn't exist
	err := os.Mkdir(workingDir, 0755)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("error creating working dir: %w", err)
	}

	// create temp dir for request and response files
	tmpPath, err := os.MkdirTemp(workingDir, "*")
	if err != nil {
		return nil, fmt.Errorf("error creating temp dir: %w", err)
	}

	// create temp files for request and response data
	reqFile, err := os.CreateTemp(tmpPath, "request-data-*")
	if err != nil {
		return nil, fmt.Errorf("error creating temp file: %w", err)
	}
	defer func() {
		if err := os.Remove(reqFile.Name()); err != nil {
			a.log.Error("failed to remove request file", zap.Error(err))
		}
	}()

	resFile, err := os.CreateTemp(tmpPath, "response-data-*")
	if err != nil {
		return nil, fmt.Errorf("error creating temp file: %w", err)
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

	message := map[string]any{
		"method": method,
		"params": data,
	}

	// write message to request file
	if err := json.NewEncoder(reqFile).Encode(message); err != nil {
		return nil, fmt.Errorf("error writing request data: %w", err)
	}

	// close & flush request file
	if err := reqFile.Close(); err != nil {
		return nil, fmt.Errorf("error closing request file: %w", err)
	}

	startParams := a.startParams

	// append req and res file names to worker arguments
	startParams.Args = append(startParams.Args, reqFile.Name(), resFile.Name())

	// ensure env is not nil
	if startParams.Env == nil {
		startParams.Env = make([]string, 0, 3)
	}

	// append req and res file names to worker env
	startParams.Env = append(startParams.Env,
		"EVAL_IO=FILE",
		"EVAL_FILE_NAME_REQUEST="+reqFile.Name(),
		"EVAL_FILE_NAME_RESPONSE="+resFile.Name(),
	)

	// create the worker with modified args and env
	worker, err := a.workerFactory(startParams)
	if err != nil {
		return nil, fmt.Errorf("error creating worker: %w", err)
	}

	// store worker for later use
	a.worker = worker

	pipe, err := worker.ReadPipe()
	if err != nil {
		return nil, fmt.Errorf("error getting read pipe: %w", err)
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
		return nil, fmt.Errorf("error starting process: %w", err)
	}

	stdoutWg.Wait()

	// wait for worker to terminate (find another way to read res earlier?)
	exitEvent, err := worker.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("error waiting for process: %w", err)
	}

	if !exitEvent.Success() {
		return nil, fmt.Errorf("process exited with non-zero code: %s", exitEvent.String())
	}

	var response map[string]any

	// read and decode response data from res file
	if err := json.NewDecoder(resFile).Decode(&response); err != nil {
		return nil, fmt.Errorf("error decoding response data: %w", err)
	}

	return response, nil
}

func (a *fileAdapter) Stop() (ReleaseFunc, error) {
	// for fileio, we already stopped the worker, as we do need to wait
	// for the process to finish in order to read the response data.
	// therefore, we don't need to do anything here.

	return noopReleaseFunc, nil
}

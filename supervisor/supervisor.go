package supervisor

import (
	"context"
	"errors"
	"sync"

	"go.uber.org/zap"
)

type Supervisor[I, O any] struct {
	persistent bool
	mode       IOMode

	sendLock sync.Mutex

	worker            Worker[I, O]
	workerLock        sync.Mutex
	workerStartParams StartParams
	workerStopParams  StopParams
	workerSendParams  SendParams

	log *zap.Logger
}

type Params struct {
	// Persistent indicates whether the underlying worker can handle
	// multiple messages, or if it is transient. Only valid for stdio
	// based workers. Default is `false`.
	Persistent bool

	// Mode describes the mode of communication between the supervisor
	// and the worker. It can be either "stdio" or "file".
	//
	// If "stdio", the supervisor will communicate with the worker over
	// stdin/stdout. The worker is expected to handle serial messages
	// from stdin and write responses stdout.
	//
	// If "file", the supervisor will communicate with the worker over
	// files. Only valid for transient workers. The name of the files
	// containing the message payload and response are passed as args
	// to the worker process.
	//
	// Default is "stdio".
	Mode IOMode

	// WorkerStartParams are the parameters to pass to the worker when
	// starting it. This can be used to pass configuration to the worker.
	WorkerStartParams StartParams

	// WorkerStopParams are the parameters to pass to the worker when
	// terminating it.
	WorkerStopParams StopParams

	// WorkerSendParams are the parameters to pass to the worker when
	// sending a message to it.
	WorkerSendParams SendParams

	// Log is the logger to use for the supervisor
	Log *zap.Logger
}

func New[I, O any](params Params) (*Supervisor[I, O], error) {
	// validate params
	if params.Mode == FileIO && params.Persistent {
		return nil, errors.New("persistent workers are not supported for file IO yet")
	}

	return &Supervisor[I, O]{
		persistent:        params.Persistent,
		mode:              params.Mode,
		workerStartParams: params.WorkerStartParams,
		workerStopParams:  params.WorkerStopParams,
		workerSendParams:  params.WorkerSendParams,
		log:               params.Log,
	}, nil
}

func (s *Supervisor[I, O]) Start(ctx context.Context) error {
	// if the worker is transient, this is a no-op
	if !s.persistent {
		return nil
	}

	// otherwise, boot the persistent worker
	_, err := s.acquireWorker(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (s *Supervisor[I, O]) Send(ctx context.Context, data I) (O, error) {
	// acquire send lock. should not be necessary as supervisors
	// are managed by a resource pool, but it does no harm to make
	// the supervisor thread-safe and serialize access.
	s.sendLock.Lock()
	defer s.sendLock.Unlock()

	var res O

	// acquire worker
	worker, err := s.acquireWorker(ctx)
	if err != nil {
		s.log.Error("error acquiring worker", zap.Error(err))
		return res, err
	}

	// send data to worker
	res, err = worker.Send(ctx, data, s.workerSendParams)
	if err != nil {
		s.log.Error("error sending data to worker", zap.Error(err))
		return res, err
	}

	return res, err
}

func (s *Supervisor[I, O]) Suspend(ctx context.Context) error {
	return s.releaseWorker(ctx)
}

func (s *Supervisor[I, O]) Shutdown(ctx context.Context) error {
	return s.terminateWorker(ctx)
}

func (s *Supervisor[I, O]) acquireWorker(ctx context.Context) (Worker[I, O], error) {
	s.workerLock.Lock()
	defer s.workerLock.Unlock()

	if s.worker != nil {
		// TODO: what if the worker is already in use, and s.persistent is false?
		return s.worker, nil
	}

	// boot a new worker
	worker, err := s.bootWorker(ctx)
	if err != nil {
		return nil, err
	}

	s.worker = worker

	return worker, nil
}

func (s *Supervisor[I, O]) releaseWorker(ctx context.Context) error {
	// if the worker is persistent, this is a no-op, as we
	// want to keep the worker alive for future messages
	if s.persistent {
		return nil
	}

	// terminate the worker
	if err := s.terminateWorker(ctx); err != nil {
		return err
	}

	return nil
}

func (s *Supervisor[I, O]) terminateWorker(ctx context.Context) error {
	s.workerLock.Lock()
	defer s.workerLock.Unlock()

	// if there is no worker, we have nothing to release
	if s.worker == nil {
		return nil
	}

	// ensure we set the worker to nil
	defer func() {
		s.worker = nil
	}()

	// TODO: what if shutdown fails? we have a zombie worker then...

	// gracefully shutdown the worker
	if err := s.worker.Terminate(); err != nil {
		s.log.Error("error terminating worker", zap.Error(err))
		// no need to wait for termination if we could not terminate
		return err
	}

	// wait for the worker to terminate
	_, err := s.worker.WaitFor(ctx, s.workerStopParams.Timeout)
	if err != nil {
		s.log.Error("error waiting for worker to terminate", zap.Error(err))
		return err
	}

	return nil
}

func (s *Supervisor[I, O]) bootWorker(ctx context.Context) (Worker[I, O], error) {
	worker := &ProcessWorker[I, O]{
		log: s.log.Named("worker"),
	}

	params := StartParams{}

	err := worker.Start(ctx, params)
	if err != nil {
		return nil, err
	}

	return worker, nil
}

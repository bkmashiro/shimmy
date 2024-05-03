package supervisor

import (
	"context"
	"sync"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"go.uber.org/zap"
)

type Supervisor[I, O any] interface {
	// Start starts the supervisor. If the supervisor is persistent,
	// this will boot the worker. If the supervisor is transient, this
	// is a no-op.
	Start(ctx context.Context) error

	// Send sends a message to the worker. If the worker is persistent,
	// this will acquire the worker and send the message. If the worker
	// is transient, this will boot a new worker, send the message, and
	// terminate the worker.
	Send(ctx context.Context, data I) (*Result[O], error)

	// Suspend suspends the worker. If the worker is persistent, this
	// will release the worker. If the worker is transient, this will
	// terminate the worker.
	Suspend(ctx context.Context) (WaitFunc, error)

	// Shutdown shuts down the worker. If the worker is persistent, this
	// will terminate the worker. If the worker is transient, this will
	// terminate the worker.
	Shutdown(ctx context.Context) (WaitFunc, error)
}

type WorkerSupervisor[I, O any] struct {
	persistent bool
	mode       IOInterface

	sendLock sync.Mutex

	worker        Adapter[I, O]
	workerFactory func(mode IOInterface) (Adapter[I, O], error)
	workerLock    sync.Mutex

	workerStartParams worker.StartConfig
	workerStopParams  worker.StopConfig
	workerSendParams  worker.SendConfig

	log *zap.Logger
}

var _ Supervisor[any, any] = (*WorkerSupervisor[any, any])(nil)

type Config[I, O any] struct {
	// Persistent indicates whether the underlying worker can handle
	// multiple messages, or if it is transient. Only valid for stdio
	// based workers. Default is `false`.
	Persistent bool `conf:"persistent"`

	// Interface describes the communication between the supervisor
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
	Interface IOInterface `conf:"interface"`

	// WorkerStartParams are the parameters to pass to the worker when
	// starting it. This can be used to pass configuration to the worker.
	WorkerStartParams worker.StartConfig `conf:"start,squash"`

	// WorkerStopParams are the parameters to pass to the worker when
	// terminating it.
	WorkerStopParams worker.StopConfig `conf:"stop"`

	// WorkerSendParams are the parameters to pass to the worker when
	// sending a message to it.
	WorkerSendParams worker.SendConfig `conf:"send"`
}

type Params[I, O any] struct {
	// Config is the config used to set up the supervisor and its workers.
	Config Config[I, O]

	// WorkerFactory the a factory function to create a new worker.
	// This is called when the supervisor needs to boot a new worker.
	WorkerFactory AdapterFactoryFn[I, O]

	// Log is the logger to use for the supervisor
	Log *zap.Logger
}

type Result[O any] struct {
	Data    O
	Release WaitFunc
}

func New[I, O any](params Params[I, O]) (Supervisor[I, O], error) {
	config := params.Config

	// validate params
	if config.Interface == FileIO && config.Persistent {
		return nil, ErrInvalidPersistentFileIO
	}

	if params.WorkerFactory == nil {
		params.WorkerFactory = defaultAdapterFactory
	}

	workerFactory := func(mode IOInterface) (Adapter[I, O], error) {
		return params.WorkerFactory(mode, params.Log)
	}

	return &WorkerSupervisor[I, O]{
		persistent:        config.Persistent,
		mode:              config.Interface,
		workerFactory:     workerFactory,
		workerStartParams: config.WorkerStartParams,
		workerStopParams:  config.WorkerStopParams,
		workerSendParams:  config.WorkerSendParams,
		log:               params.Log.Named("supervisor"),
	}, nil
}

func (s *WorkerSupervisor[I, O]) Start(ctx context.Context) error {
	// if the worker is transient, this is a no-op
	if !s.persistent {
		s.log.Debug("not starting transient worker")
		return nil
	}

	s.log.Debug("starting persistent worker")

	// otherwise, boot the persistent worker
	_, err := s.acquireWorker(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (s *WorkerSupervisor[I, O]) Send(ctx context.Context, data I) (*Result[O], error) {
	// acquire send lock. should not be necessary as supervisors
	// are managed by a resource pool, but it does no harm to make
	// the supervisor thread-safe and serialize access.
	s.sendLock.Lock()
	defer s.sendLock.Unlock()

	// acquire worker
	worker, err := s.acquireWorker(ctx)
	if err != nil {
		s.log.Error("error acquiring worker", zap.Error(err))
		return nil, err
	}

	params := s.workerSendParams
	if !s.persistent {
		params.CloseAfterSend = true
	}

	// send data to worker
	resData, err := worker.Send(ctx, data, params)
	if err != nil {
		s.log.Debug("error sending data to worker", zap.Error(err))
	}

	release, releaseErr := s.releaseWorker(ctx)
	if releaseErr != nil {
		s.log.Error("error releasing worker", zap.Error(releaseErr))
		// make release() return the release error
		release = func() error { return releaseErr }
	}

	return &Result[O]{
		Data:    resData,
		Release: release,
	}, err
}

func (s *WorkerSupervisor[I, O]) Suspend(ctx context.Context) (WaitFunc, error) {
	return s.releaseWorker(ctx)
}

func (s *WorkerSupervisor[I, O]) Shutdown(ctx context.Context) (WaitFunc, error) {
	return s.terminateWorker(ctx)
}

func (s *WorkerSupervisor[I, O]) acquireWorker(ctx context.Context) (Adapter[I, O], error) {
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

func (s *WorkerSupervisor[I, O]) releaseWorker(ctx context.Context) (WaitFunc, error) {
	// if the worker is persistent, this is a no-op, as we
	// want to keep the worker alive for future messages
	if s.persistent {
		s.log.Debug("persistent worker, not releasing")
		return noWaitFunc, nil
	}

	s.log.Debug("releasing transient worker")

	return s.terminateWorker(ctx)
}

func (s *WorkerSupervisor[I, O]) terminateWorker(ctx context.Context) (WaitFunc, error) {
	s.workerLock.Lock()
	defer s.workerLock.Unlock()

	// if there is no worker, we have nothing to release
	if s.worker == nil {
		s.log.Debug("no worker to release")
		return noWaitFunc, nil
	}

	// ensure we set the worker to nil
	defer func() {
		s.worker = nil
	}()

	return s.worker.Stop(ctx, s.workerStopParams)
}

func (s *WorkerSupervisor[I, O]) bootWorker(ctx context.Context) (Adapter[I, O], error) {
	worker, err := s.workerFactory(s.mode)
	if err != nil {
		s.log.Debug("error creating worker", zap.Error(err))
		return nil, err
	}

	err = worker.Start(ctx, s.workerStartParams)
	if err != nil {
		s.log.Debug("error starting worker", zap.Error(err))
		return nil, err
	}

	return worker, nil
}

var noWaitFunc = func() error { return nil }

package supervisor

import (
	"context"
	"fmt"
	"sync"
	"time"

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

type StartConfig = worker.StartConfig

type StopConfig = worker.StopConfig

type SendConfig struct {
	Timeout time.Duration
}

type WorkerSupervisor[I, O any] struct {
	persistent bool

	sendLock sync.Mutex

	createWorker func() (Adapter[I, O], error)

	worker     Adapter[I, O]
	workerLock sync.Mutex

	startParams StartConfig
	stopParams  StopConfig
	sendParams  SendConfig

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

	// StartParams are the parameters to pass to the worker when
	// starting it. This can be used to pass configuration to the worker.
	StartParams StartConfig `conf:"start,squash"`

	// StopParams are the parameters to pass to the worker when
	// terminating it.
	StopParams StopConfig `conf:"stop"`

	// SendParams are the parameters to pass to the worker when
	// sending a message.
	SendParams SendConfig `conf:"send"`
}

type WorkerFactoryFn func(*zap.Logger) (worker.Worker, error)

type Params[I, O any] struct {
	// Config is the config used to set up the supervisor and its workers.
	Config Config[I, O]

	// AdapterFactory is a factory function to create a new adapter. This
	// is called when the supervisor needs to create a communication adapter.
	AdapterFactory AdapterFactoryFn[I, O]

	// WorkerFactory is a factory function to create a new worker. This
	// is called when the supervisor needs to create a new worker.
	WorkerFactory WorkerFactoryFn

	// Log is the logger to use for the supervisor
	Log *zap.Logger
}

type Result[O any] struct {
	Data    O
	Release ReleaseFunc
}

func New[I, O any](params Params[I, O]) (Supervisor[I, O], error) {
	config := params.Config

	// validate params
	if config.Interface == FileIO && config.Persistent {
		return nil, ErrInvalidPersistentFileIO
	}

	if params.WorkerFactory == nil {
		params.WorkerFactory = defaultWorkerFactory
	}

	if params.AdapterFactory == nil {
		params.AdapterFactory = defaultAdapterFactory
	}

	createWorker := func() (Adapter[I, O], error) {
		worker, err := params.WorkerFactory(params.Log)
		if err != nil {
			return nil, fmt.Errorf("failed to create worker: %w", err)
		}

		adapter, err := params.AdapterFactory(
			worker,
			config.Interface,
			params.Log,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create adapter: %w", err)
		}

		return adapter, nil
	}

	return &WorkerSupervisor[I, O]{
		persistent:   config.Persistent,
		createWorker: createWorker,
		startParams:  config.StartParams,
		stopParams:   config.StopParams,
		sendParams:   config.SendParams,
		log:          params.Log.Named("supervisor"),
	}, nil
}

func (s *WorkerSupervisor[I, O]) Start(ctx context.Context) error {
	// if the worker is transient, this is a no-op
	if !s.persistent {
		s.log.Debug("start: transient, not booting worker")
		return nil
	}

	s.log.Debug("start: persistent, booting worker")

	// otherwise, boot the persistent worker
	if _, err := s.acquireWorker(ctx); err != nil {
		return fmt.Errorf("failed to start worker: %w", err)
	}

	return nil
}

func (s *WorkerSupervisor[I, O]) Send(
	ctx context.Context,
	data I,
) (*Result[O], error) {
	// acquire send lock. should not be necessary as supervisors
	// are managed by a resource pool, but it does no harm to make
	// the supervisor thread-safe and serialize access.
	s.sendLock.Lock()
	defer s.sendLock.Unlock()

	worker, err := s.acquireWorker(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire worker: %w", err)
	}

	// NOTICE: unconventional error handling, as we need to release
	// the worker before returning the error.
	resData, err := worker.Send(ctx, data, s.sendParams.Timeout)

	release, releaseErr := s.releaseWorker()
	if releaseErr != nil {
		// make release() return the release error
		release = func(context.Context) error {
			return fmt.Errorf("failed to release worker: %w", releaseErr)
		}
	}

	return &Result[O]{
		Data:    resData,
		Release: release,
	}, err
}

func (s *WorkerSupervisor[I, O]) Suspend(
	ctx context.Context,
) (WaitFunc, error) {
	release, err := s.releaseWorker()
	if err != nil {
		return nil, err
	}

	return func() error {
		return release(ctx)
	}, nil
}

func (s *WorkerSupervisor[I, O]) Shutdown(
	ctx context.Context,
) (WaitFunc, error) {
	release, err := s.terminateWorker()
	if err != nil {
		return nil, err
	}

	return func() error {
		return release(ctx)
	}, nil
}

func (s *WorkerSupervisor[I, O]) acquireWorker(
	ctx context.Context,
) (Adapter[I, O], error) {
	s.workerLock.Lock()
	defer s.workerLock.Unlock()

	if s.worker != nil {
		// TODO: what if the worker is in use, and s.persistent is false?
		return s.worker, nil
	}

	// boot a new worker
	worker, err := s.bootWorker(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to boot worker: %w", err)
	}

	s.worker = worker

	return worker, nil
}

func (s *WorkerSupervisor[I, O]) releaseWorker() (ReleaseFunc, error) {
	// if the worker is persistent, this is a no-op, as we
	// want to keep the worker alive for future messages
	if s.persistent {
		s.log.Debug("persistent worker, not releasing")
		return noopReleaseFunc, nil
	}

	s.log.Debug("releasing transient worker")

	return s.terminateWorker()
}

func (s *WorkerSupervisor[I, O]) terminateWorker() (ReleaseFunc, error) {
	s.workerLock.Lock()
	defer s.workerLock.Unlock()

	// if there is no worker, we have nothing to release
	if s.worker == nil {
		s.log.Debug("no worker to release")
		return noopReleaseFunc, nil
	}

	// ensure we set the worker to nil
	defer func() {
		s.worker = nil
	}()

	return s.worker.Stop(s.stopParams)
}

func (s *WorkerSupervisor[I, O]) bootWorker(
	ctx context.Context,
) (Adapter[I, O], error) {
	worker, err := s.createWorker()
	if err != nil {
		return nil, fmt.Errorf("failed to create worker: %w", err)
	}

	err = worker.Start(ctx, s.startParams)
	if err != nil {
		return nil, fmt.Errorf("failed to start worker: %w", err)
	}

	return worker, nil
}

func defaultWorkerFactory(log *zap.Logger) (worker.Worker, error) {
	return worker.NewProcessWorker(log), nil
}

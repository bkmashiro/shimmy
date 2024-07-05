package supervisor

import (
	"context"
	"fmt"
	"sync"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"go.uber.org/zap"
)

type Supervisor interface {
	// Start starts the supervisor. If the supervisor is persistent,
	// this will boot the worker. If the supervisor is transient, this
	// is a no-op.
	Start(ctx context.Context) error

	// Send sends a message to the worker. If the worker is persistent,
	// this will acquire the worker and send the message. If the worker
	// is transient, this will boot a new worker, send the message, and
	// terminate the worker.
	Send(ctx context.Context, method string, data map[string]any) (*Result, error)

	// Suspend suspends the worker. If the worker is persistent, this
	// will release the worker. If the worker is transient, this will
	// terminate the worker.
	Suspend(ctx context.Context) (WaitFunc, error)

	// Shutdown shuts down the worker. Both persistent and transient
	// workers will be terminated.
	Shutdown(ctx context.Context) (WaitFunc, error)
}

type WorkerSupervisor struct {
	persistent bool

	sendLock sync.Mutex

	createAdapter func() (Adapter, error)

	worker     Adapter
	workerLock sync.Mutex

	startParams StartConfig
	stopParams  StopConfig
	sendParams  SendConfig

	log *zap.Logger
}

var _ Supervisor = (*WorkerSupervisor)(nil)

type WorkerFactoryFn func(context.Context, worker.StartConfig, *zap.Logger) (worker.Worker, error)

type Params struct {
	// Config is the config used to set up the supervisor and its workers.
	Config Config

	// AdapterFactory is a factory function to create a new adapter. This
	// is called when the supervisor needs to create a communication adapter.
	AdapterFactory AdapterFactoryFn

	// WorkerFactory is a factory function to create a new worker. This
	// is called when the supervisor needs to create a new worker.
	WorkerFactory WorkerFactoryFn

	// Log is the logger to use for the supervisor
	Log *zap.Logger
}

type Result struct {
	Data    map[string]any
	Release ReleaseFunc
}

func New(params Params) (Supervisor, error) {
	config := params.Config

	if params.WorkerFactory == nil {
		params.WorkerFactory = defaultWorkerFactory
	}

	if params.AdapterFactory == nil {
		params.AdapterFactory = defaultAdapterFactory
	}

	workerFactory := func(
		ctx context.Context,
		config worker.StartConfig,
	) (worker.Worker, error) {
		return params.WorkerFactory(ctx, config, params.Log)
	}

	createAdapter := func() (Adapter, error) {
		adapter, err := params.AdapterFactory(
			workerFactory,
			config.IO,
			params.Log,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create adapter: %w", err)
		}

		return adapter, nil
	}

	// the worker is persistent if the IO interface is RPC
	persistent := config.IO.Interface == RpcIO

	return &WorkerSupervisor{
		createAdapter: createAdapter,
		persistent:    persistent,
		startParams:   config.StartParams,
		stopParams:    config.StopParams,
		sendParams:    config.SendParams,
		log:           params.Log.Named("supervisor"),
	}, nil
}

func (s *WorkerSupervisor) Start(ctx context.Context) error {
	// if the worker is transient, this is a no-op
	if !s.persistent {
		return nil
	}

	// otherwise, boot the persistent worker
	if _, err := s.acquireWorker(ctx); err != nil {
		return fmt.Errorf("failed to start worker: %w", err)
	}

	return nil
}

func (s *WorkerSupervisor) Send(
	ctx context.Context,
	method string,
	data map[string]any,
) (*Result, error) {
	// acquire send lock. should not be necessary as supervisors
	// are managed by a resource pool, but it does no harm to make
	// the supervisor thread-safe and serialize access.
	s.sendLock.Lock()
	defer s.sendLock.Unlock()

	worker, err := s.acquireWorker(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire worker: %w", err)
	}

	// NOTICE: unconventional error handling ahead, as we need
	//         to release the worker before returning the error.
	resData, err := worker.Send(ctx, method, data, s.sendParams.Timeout)

	release, releaseErr := s.releaseWorker()
	if releaseErr != nil {
		// make release() return the release error
		release = func(context.Context) error {
			return fmt.Errorf("failed to release worker: %w", releaseErr)
		}
	}

	return &Result{
		Data:    resData,
		Release: release,
	}, err
}

func (s *WorkerSupervisor) Suspend(
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

func (s *WorkerSupervisor) Shutdown(
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

func (s *WorkerSupervisor) acquireWorker(
	ctx context.Context,
) (Adapter, error) {
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

func (s *WorkerSupervisor) releaseWorker() (ReleaseFunc, error) {
	// if the worker is persistent, this is a no-op, as we
	// want to keep the worker alive for future messages
	if s.persistent {
		return noopReleaseFunc, nil
	}

	s.log.Debug("transient: releasing worker")

	return s.terminateWorker()
}

func (s *WorkerSupervisor) terminateWorker() (ReleaseFunc, error) {
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

func (s *WorkerSupervisor) bootWorker(ctx context.Context) (Adapter, error) {
	adapter, err := s.createAdapter()
	if err != nil {
		return nil, fmt.Errorf("failed to create worker: %w", err)
	}

	if err = adapter.Start(ctx, s.startParams); err != nil {
		return nil, fmt.Errorf("failed to start worker: %w", err)
	}

	return adapter, nil
}

func defaultWorkerFactory(
	ctx context.Context,
	config worker.StartConfig,
	log *zap.Logger,
) (worker.Worker, error) {
	return worker.NewProcessWorker(ctx, config, log), nil
}

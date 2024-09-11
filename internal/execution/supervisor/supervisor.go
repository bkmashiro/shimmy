package supervisor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
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

type workerRef struct {
	cancel context.CancelFunc
	worker Adapter
}

type WorkerSupervisor struct {
	persistent bool

	sendLock sync.Mutex

	createWorker func() (*workerRef, error)

	workerRef  *workerRef
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

	// Context is the context to use for the supervisor
	Context context.Context

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

	createAdapter := func() (*workerRef, error) {
		workerCtx, cancel := context.WithCancel(params.Context)

		workerFactory := func(config worker.StartConfig) (worker.Worker, error) {
			return params.WorkerFactory(workerCtx, config, params.Log)
		}

		adapter, err := params.AdapterFactory(
			workerFactory,
			config.IO,
			params.Log,
		)
		if err != nil {
			defer cancel()
			return nil, fmt.Errorf("failed to create adapter: %w", err)
		}

		return &workerRef{
			worker: adapter,
			cancel: cancel,
		}, nil
	}

	// the worker is persistent if the IO interface is RPC
	persistent := config.IO.Interface == RpcIO

	return &WorkerSupervisor{
		createWorker: createAdapter,
		persistent:   persistent,
		startParams:  config.StartParams,
		stopParams:   config.StopParams,
		sendParams:   config.SendParams,
		log:          params.Log.Named("supervisor"),
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

func (s *WorkerSupervisor) Suspend(ctx context.Context) (WaitFunc, error) {
	release, err := s.releaseWorker()
	if err != nil {
		return nil, err
	}

	return func() error {
		return release(ctx)
	}, nil
}

func (s *WorkerSupervisor) Shutdown(ctx context.Context) (WaitFunc, error) {
	release, err := s.terminateWorker()
	if err != nil {
		return nil, err
	}

	return func() error {
		return release(ctx)
	}, nil
}

func (s *WorkerSupervisor) acquireWorker(ctx context.Context) (Adapter, error) {
	s.workerLock.Lock()
	defer s.workerLock.Unlock()

	if s.workerRef != nil {
		// TODO: what if the worker is in use, and s.persistent is false?
		return s.workerRef.worker, nil
	}

	// boot a new worker
	ref, err := s.bootWorker(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to boot worker: %w", err)
	}

	s.workerRef = ref

	return ref.worker, nil
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
	if s.workerRef == nil {
		s.log.Debug("no worker to release")
		return noopReleaseFunc, nil
	}

	// ensure we set the reference to the worker to nil
	defer func() {
		s.workerRef = nil
	}()

	wait, err := s.workerRef.worker.Stop()
	if err != nil {
		// if we fail to stop the worker, we'll still cancel the
		// worker context to ensure the worker is terminated.
		s.workerRef.cancel()
		return nil, err
	}

	// keep a reference to the worker context cancel function
	cancel := s.workerRef.cancel

	return func(ctx context.Context) error {
		done := make(chan struct{})
		defer close(done)

		go func() {
			// cancel the worker context if either the wait function
			// returns, or the stop timeout is reached. this way we
			// give the worker a chance to stop gracefully, while
			// ensuring it is terminated eventually.
			defer cancel()

			select {
			case <-done:
				// worker has stopped or context is done
				return
			case <-time.After(s.stopParams.Timeout):
				// worker stop timeout reached
				return
			}
		}()

		// wait for the worker to stop, or until the context is done
		// either way, the wait function will return eventually.
		return wait(ctx)
	}, nil
}

func (s *WorkerSupervisor) bootWorker(ctx context.Context) (*workerRef, error) {
	ref, err := s.createWorker()
	if err != nil {
		return nil, fmt.Errorf("failed to create worker: %w", err)
	}

	if err = ref.worker.Start(ctx, s.startParams); err != nil {
		return nil, fmt.Errorf("failed to start worker: %w", err)
	}

	return ref, nil
}

func defaultWorkerFactory(
	ctx context.Context,
	config worker.StartConfig,
	log *zap.Logger,
) (worker.Worker, error) {
	return worker.NewProcessWorker(ctx, config, log), nil
}

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
)

type Supervisor interface {
	// Start starts the supervisor. Persistent and eager supervisors boot a
	// worker process; ordinary transient supervisors are no-ops until Send.
	Start(ctx context.Context) error

	// Send sends a message to the worker. If the worker is persistent,
	// this will acquire the worker and send the message. If the worker
	// is transient, this will boot a new worker, send the message, and
	// terminate the worker. Eager workers are never reused after Send and a
	// clean replacement is started asynchronously.
	Send(ctx context.Context, method string, data map[string]any) (*Result, error)

	// Suspend suspends the worker. If the worker is persistent, this
	// will release the worker. If the worker is transient, this will
	// terminate the worker. Eager supervisors fail closed because suspending
	// during an asynchronous refill would violate the one-slot lifecycle.
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
	persistent          bool
	releaseBeforeReturn bool
	eager               bool
	lifecycleCtx        context.Context

	sendLock sync.Mutex

	createWorker func() (*workerRef, error)

	workerRef  *workerRef
	workerLock sync.Mutex
	closed     bool

	eagerRefillDone chan struct{}
	eagerRefillErr  error
	eagerRefillWG   sync.WaitGroup

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

	// RPC historically owns one persistent worker, while file IO creates one
	// worker per message. Isolation wrappers may explicitly narrow RPC to an
	// invocation-scoped lifecycle without changing its transport.
	persistent := config.IO.Interface == RpcIO
	switch config.WorkerLifecycle {
	case WorkerLifecycleAutomatic:
	case WorkerLifecyclePersistent:
		if config.IO.Interface != RpcIO {
			return nil, fmt.Errorf("persistent worker lifecycle requires rpc interface")
		}
		persistent = true
	case WorkerLifecycleInvocation:
		persistent = false
	case WorkerLifecycleEager:
		if config.IO.Interface != RpcIO {
			return nil, fmt.Errorf("eager worker lifecycle requires rpc interface")
		}
		persistent = false
	default:
		return nil, fmt.Errorf("unsupported worker lifecycle %q", config.WorkerLifecycle)
	}

	return &WorkerSupervisor{
		createWorker:        createAdapter,
		persistent:          persistent,
		releaseBeforeReturn: config.WorkerLifecycle == WorkerLifecycleInvocation,
		eager:               config.WorkerLifecycle == WorkerLifecycleEager,
		lifecycleCtx:        params.Context,
		startParams:         config.StartParams,
		stopParams:          config.StopParams,
		sendParams:          config.SendParams,
		log:                 params.Log.Named("supervisor"),
	}, nil
}

func (s *WorkerSupervisor) Start(ctx context.Context) error {
	// Ordinary transient workers start on demand. Eager workers spawn their
	// first clean process during startup so it can initialize before traffic.
	if !s.persistent && !s.eager {
		return nil
	}

	// Boot the persistent worker or first eager replacement.
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

	var release ReleaseFunc
	var releaseErr error
	if s.eager {
		// Detach the served worker while sendLock is still held. The refill is
		// asynchronous, but no concurrent Send can observe or reuse this worker.
		releaseErr = s.scheduleEagerRefill()
		release = noopReleaseFunc
	} else {
		release, releaseErr = s.releaseWorker()
	}
	if s.releaseBeforeReturn && releaseErr == nil {
		if release != nil {
			releaseErr = release(context.Background())
		}
		release = noopReleaseFunc
	}
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
	if s.eager {
		return nil, errors.New("eager worker supervisor cannot be suspended; use Shutdown")
	}
	release, err := s.releaseWorker()
	if err != nil {
		return nil, err
	}

	return func() error {
		return release(ctx)
	}, nil
}

func (s *WorkerSupervisor) Shutdown(ctx context.Context) (WaitFunc, error) {
	// Do not stop or detach the worker while an evaluation is using it. For
	// eager workers this also orders WaitGroup.Add before shutdown begins Wait.
	s.sendLock.Lock()
	defer s.sendLock.Unlock()

	if s.eager {
		return s.shutdownEager(ctx)
	}
	release, err := s.terminateWorker()
	if err != nil {
		return nil, err
	}

	return func() error {
		return release(ctx)
	}, nil
}

func (s *WorkerSupervisor) acquireWorker(ctx context.Context) (Adapter, error) {
	for {
		s.workerLock.Lock()
		if s.closed {
			s.workerLock.Unlock()
			return nil, errors.New("worker supervisor is shut down")
		}
		if s.workerRef != nil {
			worker := s.workerRef.worker
			s.workerLock.Unlock()
			return worker, nil
		}
		refillDone := s.eagerRefillDone
		if refillDone != nil {
			select {
			case <-refillDone:
				refillErr := s.eagerRefillErr
				s.eagerRefillDone = nil
				s.eagerRefillErr = nil
				s.workerLock.Unlock()
				if refillErr != nil {
					return nil, fmt.Errorf("eager worker refill failed: %w", refillErr)
				}
				continue
			default:
				s.workerLock.Unlock()
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-refillDone:
					continue
				}
			}
		}

		// Send is serialized, so this synchronous boot cannot race another
		// request. Keep workerLock held so Shutdown cannot strand a new worker.
		ref, err := s.bootWorker(ctx)
		if err != nil {
			s.workerLock.Unlock()
			return nil, fmt.Errorf("failed to boot worker: %w", err)
		}
		s.workerRef = ref
		s.workerLock.Unlock()
		return ref.worker, nil
	}
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
	ref := s.workerRef
	s.workerRef = nil
	s.workerLock.Unlock()

	if ref == nil {
		s.log.Debug("no worker to release")
		return noopReleaseFunc, nil
	}
	return s.stopWorker(ref)
}

func (s *WorkerSupervisor) stopWorker(ref *workerRef) (ReleaseFunc, error) {
	wait, err := ref.worker.Stop()
	if err != nil {
		// If Stop fails, cancel the owned worker context to ensure termination.
		ref.cancel()
		return nil, err
	}

	cancel := ref.cancel
	if wait == nil {
		cancel()
		return noopReleaseFunc, nil
	}

	return func(context.Context) error {
		done := make(chan struct{})
		defer close(done)

		go func() {
			// Cancel the worker context if either wait returns or the stop
			// timeout is reached. The wait itself deliberately ignores the
			// request context: isolation must prove the old process exited
			// before a clean replacement can start.
			defer cancel()

			select {
			case <-done:
				return
			case <-time.After(s.stopParams.Timeout):
				return
			}
		}()

		return wait(context.Background())
	}, nil
}

func (s *WorkerSupervisor) scheduleEagerRefill() error {
	s.workerLock.Lock()
	if s.closed {
		s.workerLock.Unlock()
		return errors.New("worker supervisor is shut down")
	}
	if s.workerRef == nil {
		s.workerLock.Unlock()
		return errors.New("eager worker is missing")
	}
	served := s.workerRef
	s.workerRef = nil
	done := make(chan struct{})
	s.eagerRefillDone = done
	s.eagerRefillErr = nil
	s.eagerRefillWG.Add(1)
	s.workerLock.Unlock()

	go func() {
		defer s.eagerRefillWG.Done()
		var refillErr error
		release, stopErr := s.stopWorker(served)
		if stopErr != nil {
			refillErr = stopErr
		} else if release != nil {
			refillErr = release(context.Background())
		}

		var replacement *workerRef
		s.workerLock.Lock()
		closed := s.closed
		s.workerLock.Unlock()
		if refillErr == nil && !closed {
			replacement, refillErr = s.bootWorker(s.lifecycleCtx)
		}

		s.workerLock.Lock()
		closed = s.closed
		if refillErr == nil && !closed {
			s.workerRef = replacement
			replacement = nil
		}
		s.eagerRefillErr = refillErr
		close(done)
		s.workerLock.Unlock()

		// Shutdown may race a boot that was already in progress. Never publish
		// that replacement; stop it before the refill goroutine exits.
		if replacement != nil {
			release, stopErr := s.stopWorker(replacement)
			if stopErr == nil && release != nil {
				_ = release(context.Background())
			}
		}
	}()
	return nil
}

func (s *WorkerSupervisor) shutdownEager(ctx context.Context) (WaitFunc, error) {
	s.workerLock.Lock()
	s.closed = true
	ref := s.workerRef
	s.workerRef = nil
	s.workerLock.Unlock()

	release := noopReleaseFunc
	if ref != nil {
		var err error
		release, err = s.stopWorker(ref)
		if err != nil {
			return nil, err
		}
	}
	return func() error {
		releaseErr := release(ctx)
		refillDone := make(chan struct{})
		go func() {
			s.eagerRefillWG.Wait()
			close(refillDone)
		}()
		select {
		case <-ctx.Done():
			return errors.Join(releaseErr, ctx.Err())
		case <-refillDone:
			return releaseErr
		}
	}, nil
}

func (s *WorkerSupervisor) bootWorker(ctx context.Context) (*workerRef, error) {
	ref, err := s.createWorker()
	if err != nil {
		return nil, fmt.Errorf("failed to create worker: %w", err)
	}

	if err = ref.worker.Start(ctx, s.startParams); err != nil {
		ref.cancel()
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

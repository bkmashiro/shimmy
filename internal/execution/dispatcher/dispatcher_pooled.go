package dispatcher

import (
	"context"
	"fmt"
	"runtime"

	"github.com/jackc/puddle/v2"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

type PooledDispatcher struct {
	ctx  context.Context
	pool *puddle.Pool[supervisor.Supervisor]
	log  *zap.Logger
}

var _ Dispatcher = (*PooledDispatcher)(nil)

type PooledDispatcherConfig struct {
	// MaxWorkers is the maximum number of concurrent workers
	MaxWorkers int `conf:"max_workers"`

	// SupervisorConfig is the configuration to use for the supervisor
	Supervisor supervisor.Config `conf:"supervisor,squash"`
}

type PooledDispatcherParams struct {
	// Config is the config for the dispatcher and the underlying supervisors
	Config PooledDispatcherConfig

	// Context is the context to use for the dispatcher
	Context context.Context

	// SupervisorFactory is the factory function to create a new supervisor
	SupervisorFactory SupervisorFactory

	// Log is the logger to use for the dispatcher
	Log *zap.Logger
}

func NewPooledDispatcher(
	params PooledDispatcherParams,
) (Dispatcher, error) {
	if params.SupervisorFactory == nil {
		params.SupervisorFactory = defaultSupervisorFactory
	}

	pool, err := createPool(params)
	if err != nil {
		return nil, err
	}

	return &PooledDispatcher{
		pool: pool,
		ctx:  params.Context,
		log:  params.Log.Named("dispatcher_pooled"),
	}, nil
}

func (m *PooledDispatcher) Start(context.Context) error {
	// starting the pool is a no-op
	return nil
}

func (m *PooledDispatcher) Send(
	ctx context.Context,
	method string,
	data map[string]any,
) (map[string]any, error) {

	resource, err := m.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("error acquiring supervisor: %w", err)
	}

	result, err := m.sendToSupervisor(ctx, method, data, resource)
	if err != nil {
		return nil, fmt.Errorf("error sending data: %w", err)
	}

	return result, nil
}

func (m *PooledDispatcher) sendToSupervisor(
	ctx context.Context,
	method string,
	data map[string]any,
	resource *puddle.Resource[supervisor.Supervisor],
) (map[string]any, error) {
	var err error
	var res *supervisor.Result

	destroyOrRelease := func() {
		if err != nil {
			m.log.Debug("destroying supervisor due to error")
			resource.Destroy()
		} else {
			m.log.Debug("releasing supervisor back to pool")
			resource.Release()
		}
	}

	dispose := func() {
		// if there is no wait function, we can release the resource
		if res == nil || res.Release == nil {
			destroyOrRelease()
			return
		}

		// if there is an error destroying the supervisor, we need to
		// log it and destroy the resource
		if releaseErr := res.Release(m.ctx); releaseErr != nil {
			m.log.Error("destroying supervisor due to error waiting", zap.Error(releaseErr))
			resource.Destroy()
			return
		}

		// if the supervisor was suspended, we release the resource
		destroyOrRelease()
	}

	defer func() {
		// we need to release the supervisor back to the pool after
		// we're done with it. however, we want to return from the
		// function before releasing the supervisor, as we don't want
		// to block the caller. therefore, we release the supervisor
		// in a goroutine.
		go dispose()
	}()

	supervisor := resource.Value()

	res, err = supervisor.Send(ctx, method, data)
	if err != nil {
		return nil, err
	}

	return res.Data, nil
}

// Shutdown stops the dispatcher and waits for all workers to finish.
func (m *PooledDispatcher) Shutdown(context.Context) error {
	m.log.Debug("shutting down")
	m.pool.Close()
	return nil
}

// MARK: - Pool

func createPool(
	params PooledDispatcherParams,
) (*puddle.Pool[supervisor.Supervisor], error) {
	log := params.Log.Named("dispatcher_pool")

	constructor := func(ctx context.Context) (supervisor.Supervisor, error) {
		sv, err := params.SupervisorFactory(supervisor.Params{
			Context: ctx,
			Config:  params.Config.Supervisor,
			Log:     params.Log,
		})
		if err != nil {
			return nil, err
		}

		if err = sv.Start(ctx); err != nil {
			return nil, err
		}

		return sv, nil
	}

	destructor := func(s supervisor.Supervisor) {
		wait, err := s.Shutdown(params.Context)
		if err != nil {
			log.Error("error shutting down supervisor", zap.Error(err))
			return
		}

		if wait == nil {
			log.Warn("supervisor did not return a wait function")
			return
		}

		if err := wait(); err != nil {
			log.Error("error waiting for supervisor to shut down", zap.Error(err))
		}
	}

	// if the max workers is less than or equal to 0,
	// default to the number of logical CPUs
	maxWorkers := params.Config.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = runtime.NumCPU()
	}

	return puddle.NewPool(&puddle.Config[supervisor.Supervisor]{
		Constructor: constructor,
		Destructor:  destructor,
		MaxSize:     int32(maxWorkers),
	})
}

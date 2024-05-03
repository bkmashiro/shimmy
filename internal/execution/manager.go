package execution

import (
	"context"
	"fmt"

	"github.com/jackc/puddle/v2"
	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
	"go.uber.org/zap"
)

type Manager[I, O any] interface {
	// Send sends data to a supervisor and returns the result
	Send(ctx context.Context, data I) (O, error)

	// Shutdown stops the manager and waits for all workers to finish.
	Shutdown()
}

type WorkerManager[I, O any] struct {
	pool *puddle.Pool[supervisor.Supervisor[I, O]]
	log  *zap.Logger
}

var _ Manager[any, any] = (*WorkerManager[any, any])(nil)

type SupervisorFactory[I, O any] func(supervisor.Params[I, O]) (supervisor.Supervisor[I, O], error)

type Config[I, O any] struct {
	// MaxWorkers is the maximum number of concurrent workers
	MaxWorkers int `conf:"max_workers"`

	// SupervisorConfig is the configuration to use for the supervisor
	Supervisor supervisor.Config[I, O] `conf:"supervisor,squash"`
}

type Params[I, O any] struct {
	// Context is the context to use for the manager
	Context context.Context

	// Config is the config for the manager and the underlying supervisors
	Config Config[I, O]

	// SupervisorFactory is the factory function to create a new supervisor
	SupervisorFactory SupervisorFactory[I, O]

	// Log is the logger to use for the manager
	Log *zap.Logger
}

func NewManager[I, O any](params Params[I, O]) (*WorkerManager[I, O], error) {
	log := params.Log.Named("manager")

	if params.SupervisorFactory == nil {
		params.SupervisorFactory = defaultSupervisorFactory
	}

	pool, err := createPool(params)
	if err != nil {
		return nil, err
	}

	return &WorkerManager[I, O]{
		pool: pool,
		log:  log,
	}, nil
}

func (m *WorkerManager[I, O]) Send(ctx context.Context, data I) (O, error) {

	m.log.Debug("sending message, acquiring supervisor from pool")

	resource, err := m.pool.Acquire(ctx)
	if err != nil {
		m.log.Debug("error acquiring supervisor", zap.Error(err))
		var zero O
		return zero, fmt.Errorf("error acquiring supervisor: %w", err)
	}

	m.log.Debug("supervisor acquired, sending message")

	res, err := m.sendToSupervisor(ctx, data, resource)
	if err != nil {
		m.log.Debug("error sending data to supervisor", zap.Error(err))
		var zero O
		return zero, fmt.Errorf("error sending data: %w", err)
	}

	m.log.Debug("message sent to supervisor")

	return res, nil
}

func (m *WorkerManager[I, O]) sendToSupervisor(
	ctx context.Context,
	data I,
	resource *puddle.Resource[supervisor.Supervisor[I, O]],
) (O, error) {
	var err error
	var res *supervisor.Result[O]

	dispose := func() {
		// if there was an error while handling the message,
		// we need to destroy the supervisor
		// if err != nil {
		// 	resource.Destroy()
		// 	return
		// }

		destroyOrRelease := func() {
			if err != nil {
				m.log.Debug("destroying supervisor: error")
				resource.Destroy()
			} else {
				m.log.Debug("releasing supervisor: suspended")
				resource.Release()
			}
		}

		// if there is no wait function, we can release the resource
		if res == nil || res.Release == nil {
			destroyOrRelease()
			return
		}

		// if there is an error destroying the supervisor, we need to
		// log it and destroy the resource
		if releaseErr := res.Release(); releaseErr != nil {
			m.log.Error("destroying supervisor: error waiting", zap.Error(releaseErr))
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

	res, err = supervisor.Send(ctx, data)
	if err != nil {
		m.log.Error("error sending data to supervisor", zap.Error(err))
		var zero O
		return zero, err
	}

	return res.Data, nil
}

// Shutdown stops the manager and waits for all workers to finish.
func (m *WorkerManager[I, O]) Shutdown() {
	m.log.Debug("shutting down manager")
	m.pool.Close()
}

// MARK: - Pool

func createPool[I, O any](params Params[I, O]) (*puddle.Pool[supervisor.Supervisor[I, O]], error) {
	log := params.Log.Named("manager_pool")

	constructor := func(ctx context.Context) (supervisor.Supervisor[I, O], error) {
		sv, err := params.SupervisorFactory(supervisor.Params[I, O]{
			Config: params.Config.Supervisor,
			Log:    params.Log,
		})
		if err != nil {
			return nil, err
		}

		log.Debug("booting supervisor")

		if err = sv.Start(ctx); err != nil {
			return nil, err
		}

		log.Debug("done booting supervisor")

		return sv, nil
	}

	destructor := func(s supervisor.Supervisor[I, O]) {
		log.Debug("shutting down supervisor")

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

		log.Debug("supervisor shut down")
	}

	return puddle.NewPool(&puddle.Config[supervisor.Supervisor[I, O]]{
		Constructor: constructor,
		Destructor:  destructor,
		MaxSize:     int32(params.Config.MaxWorkers),
	})
}

func defaultSupervisorFactory[I, O any](params supervisor.Params[I, O]) (supervisor.Supervisor[I, O], error) {
	return supervisor.New(params)
}

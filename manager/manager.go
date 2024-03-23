package manager

import (
	"context"

	"github.com/jackc/puddle/v2"
	"github.com/lambda-feedback/shimmy/supervisor"
	"go.uber.org/zap"
)

type Manager[I, O any] struct {
	pool *puddle.Pool[supervisor.Supervisor[I, O]]
	log  *zap.Logger
}

type SupervisorFactory[I, O any] func(supervisor.SupervisorConfig[I, O]) (supervisor.Supervisor[I, O], error)

func defaultSupervisorFactory[I, O any](params supervisor.SupervisorConfig[I, O]) (supervisor.Supervisor[I, O], error) {
	return supervisor.New(params)
}

type ManagerConfig[I, O any] struct {
	supervisor.SupervisorConfig[I, O]

	// Context is the context to use for the manager
	Context context.Context

	// MaxCapacity is the maximum number of tasks that can be queued
	MaxCapacity int

	// SupervisorFactory is the factory function to create a new supervisor
	SupervisorFactory SupervisorFactory[I, O]

	// Log is the logger to use for the manager
	Log *zap.Logger
}

func New[I, O any](params ManagerConfig[I, O]) (*Manager[I, O], error) {
	log := params.Log.Named("manager")

	if params.SupervisorFactory == nil {
		params.SupervisorFactory = defaultSupervisorFactory
	}

	pool, err := createPool(params)
	if err != nil {
		return nil, err
	}

	return &Manager[I, O]{
		pool: pool,
		log:  log,
	}, nil
}

func (m *Manager[I, O]) Send(ctx context.Context, data I) (O, error) {
	resource, err := m.pool.Acquire(ctx)
	if err != nil {
		m.log.Error("error acquiring supervisor", zap.Error(err))
		var zero O
		return zero, err
	}

	sup := resource.Value()

	var res *supervisor.Result[O]

	release := func() {
		if res == nil || res.Wait == nil {
			// if there is no wait function, we can release the resource
			resource.Release()
			return
		}

		if err := res.Wait(); err != nil {
			// if there is an error destroying the supervisor, we need to
			// log it and destroy the resource
			m.log.Error("error suspending supervisor", zap.Error(err))
			resource.Destroy()
			return
		}

		// if the supervisor was suspended, we release the resource
		resource.Release()
	}

	defer func() {
		// we need to release the supervisor back to the pool after
		// we're done with it. however, we want to return from the
		// function before releasing the supervisor, as we don't want
		// to block the caller. therefore, we release the supervisor
		// in a goroutine.
		go release()
	}()

	res, err = sup.Send(ctx, data)
	if err != nil {
		// TODO: probably destroy the resource here in case it is a unrecoverable error
		m.log.Error("error sending data to supervisor", zap.Error(err))
		var zero O
		return zero, err
	}

	return res.Data, nil
}

// Shutdown stops the manager and waits for all workers to finish.
func (m *Manager[I, O]) Shutdown() {
	m.pool.Close()
}

// MARK: - Pool

func createPool[I, O any](params ManagerConfig[I, O]) (*puddle.Pool[supervisor.Supervisor[I, O]], error) {
	constructor := func(ctx context.Context) (supervisor.Supervisor[I, O], error) {
		supervisorConfig := params.SupervisorConfig

		// make sure the supervisor has a logger
		if supervisorConfig.Log == nil {
			supervisorConfig.Log = params.Log
		}

		sv, err := params.SupervisorFactory(params.SupervisorConfig)
		if err != nil {
			return nil, err
		}

		if err = sv.Start(ctx); err != nil {
			return nil, err
		}

		return sv, nil
	}

	destructor := func(s supervisor.Supervisor[I, O]) {
		wait, err := s.Shutdown(params.Context)
		if err != nil {
			params.Log.Error("error shutting down supervisor", zap.Error(err))
			return
		}

		if wait == nil {
			params.Log.Warn("supervisor did not return a wait function")
			return
		}

		if err := wait(); err != nil {
			params.Log.Error("error waiting for supervisor to shut down", zap.Error(err))
		}
	}

	return puddle.NewPool(&puddle.Config[supervisor.Supervisor[I, O]]{
		Constructor: constructor,
		Destructor:  destructor,
		MaxSize:     int32(params.MaxCapacity),
	})
}

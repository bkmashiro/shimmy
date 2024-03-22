package manager

import (
	"context"

	"github.com/jackc/puddle/v2"
	"github.com/lambda-feedback/shimmy/supervisor"
	"go.uber.org/zap"
)

type Manager[I, O any] struct {
	pool *puddle.Pool[*supervisor.Supervisor[I, O]]
	log  *zap.Logger
}

type ManagerConfig[I, O any] struct {
	supervisor.SupervisorConfig[I, O]

	// Context is the context to use for the manager
	Context context.Context

	// MaxCapacity is the maximum number of tasks that can be queued
	MaxCapacity int

	// Log is the logger to use for the manager
	Log *zap.Logger
}

func New[I, O any](params ManagerConfig[I, O]) (*Manager[I, O], error) {
	log := params.Log.Named("manager")

	pool, err := createPool[I, O](
		params.Context,
		params.MaxCapacity,
		params.SupervisorConfig,
		log,
	)
	if err != nil {
		return nil, err
	}

	return &Manager[I, O]{
		pool: pool,
		log:  log,
	}, nil
}

func (m *Manager[I, O]) Send(ctx context.Context, data I) (O, error) {
	var res supervisor.Result[O]

	resource, err := m.pool.Acquire(ctx)
	if err != nil {
		m.log.Error("error acquiring supervisor", zap.Error(err))
		return res.Data, err
	}

	sup := resource.Value()

	release := func() {
		if res.Wait == nil {
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
		return res.Data, err
	}

	return res.Data, nil
}

// Shutdown stops the manager and waits for all workers to finish.
func (m *Manager[I, O]) Shutdown() {
	m.pool.Close()
}

// MARK: - Pool

func createPool[I, O any](
	ctx context.Context,
	maxSize int,
	params supervisor.SupervisorConfig[I, O],
	log *zap.Logger,
) (*puddle.Pool[*supervisor.Supervisor[I, O]], error) {
	constructor := func(ctx context.Context) (*supervisor.Supervisor[I, O], error) {
		// make sure the supervisor has a logger
		params.Log = log

		sv, err := supervisor.New[I, O](params)
		if err != nil {
			return nil, err
		}

		if err = sv.Start(ctx); err != nil {
			return nil, err
		}

		return sv, nil
	}

	destructor := func(s *supervisor.Supervisor[I, O]) {
		wait, err := s.Shutdown(ctx)
		if err != nil {
			log.Error("error shutting down supervisor", zap.Error(err))
		}

		if err := wait(); err != nil {
			log.Error("error waiting for supervisor to shut down", zap.Error(err))
		}
	}

	return puddle.NewPool(&puddle.Config[*supervisor.Supervisor[I, O]]{
		Constructor: constructor,
		Destructor:  destructor,
		MaxSize:     int32(maxSize),
	})
}

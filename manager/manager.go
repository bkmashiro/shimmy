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

type ManagerParams struct {
	supervisor.SupervisorConfig

	// Context is the context to use for the manager
	Context context.Context

	// MaxCapacity is the maximum number of tasks that can be queued
	MaxCapacity int

	// Log is the logger to use for the manager
	Log *zap.Logger
}

func New[I, O any](params ManagerParams) (*Manager[I, O], error) {
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
	var res O

	resource, err := m.pool.Acquire(ctx)
	if err != nil {
		m.log.Error("error acquiring supervisor", zap.Error(err))
		return res, err
	}

	sup := resource.Value()

	release := func() {
		if err := sup.Suspend(ctx); err != nil {
			m.log.Error("error suspending supervisor", zap.Error(err))
			resource.Destroy()
			return
		}

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
		return res, err
	}

	return res, nil
}

// Shutdown stops the manager and waits for all workers to finish.
func (m *Manager[I, O]) Shutdown() {
	m.pool.Close()
}

// MARK: - Pool

func createPool[I, O any](
	ctx context.Context,
	maxSize int,
	params supervisor.SupervisorConfig,
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
		if err := s.Shutdown(ctx); err != nil {
			log.Error("error shutting down supervisor", zap.Error(err))
		}
	}

	return puddle.NewPool(&puddle.Config[*supervisor.Supervisor[I, O]]{
		Constructor: constructor,
		Destructor:  destructor,
		MaxSize:     int32(maxSize),
	})
}

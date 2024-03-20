package manager

import (
	"context"

	"github.com/jackc/puddle/v2"
	"github.com/lambda-feedback/shimmy/supervisor"
	"go.uber.org/zap"
)

type Manager[T any] struct {
	pool *puddle.Pool[*supervisor.Supervisor[T]]
	log  *zap.Logger
}

type ManagerParams struct {
	// MaxCapacity is the maximum number of tasks that can be queued
	MaxCapacity int

	// Log is the logger to use for the manager
	Log *zap.Logger
}

func New[T any](params ManagerParams) (*Manager[T], error) {
	log := params.Log.Named("manager")

	pool, err := createPool[T](
		params.MaxCapacity,
		log,
	)
	if err != nil {
		return nil, err
	}

	return &Manager[T]{
		pool: pool,
		log:  log,
	}, nil
}

// Shutdown stops the manager and waits for all workers to finish.
func (m *Manager[T]) Shutdown() {
	m.pool.Close()
}

// func (m *Manager[T]) Send(data T) {
// 	m.pool.
// }

// MARK: - Pool

func createPool[T any](
	maxSize int,
	log *zap.Logger,
) (*puddle.Pool[*supervisor.Supervisor[T]], error) {
	constructor := func(ctx context.Context) (*supervisor.Supervisor[T], error) {
		return supervisor.New[T](supervisor.Params{
			Log: log, // TODO: rename
		}), nil
	}

	destructor := func(s *supervisor.Supervisor[T]) {
		if err := s.Shutdown(); err != nil {
			log.Error("error shutting down supervisor", zap.Error(err))
		}
	}

	return puddle.NewPool(&puddle.Config[*supervisor.Supervisor[T]]{
		Constructor: constructor,
		Destructor:  destructor,
		MaxSize:     int32(maxSize),
	})
}

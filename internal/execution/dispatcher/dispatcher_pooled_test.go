package dispatcher_test

import (
	"context"
	"testing"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/dispatcher"
	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

func TestPooledDispatcher_New_FailsInvalidCapacity(t *testing.T) {
	m, err := dispatcher.NewPooledDispatcher(dispatcher.PooledDispatcherParams[any, any]{
		Config: dispatcher.PooledDispatcherConfig[any, any]{
			MaxWorkers: 0,
		},
		Context: context.Background(),
		Log:     zap.NewNop(),
	})
	assert.Error(t, err)
	assert.Nil(t, m)
}

func TestPooledDispatcher_New_CreatesNewDispatcher(t *testing.T) {
	m, _, err := createPooledDispatcher(t)

	assert.NoError(t, err)
	assert.NotNil(t, m)
}

func TestPooledDispatcher_Send(t *testing.T) {
	m, sv, _ := createPooledDispatcher(t)

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Send(mock.Anything, "data").Return(&supervisor.Result[any]{Data: "data"}, nil)

	_, err := m.Send(context.Background(), "data")
	assert.NoError(t, err)
}

func TestPooledDispatcher_Send_FailsToAcquireSupervisor(t *testing.T) {
	factory := func(params supervisor.Params[any, any]) (supervisor.Supervisor[any, any], error) {
		return nil, assert.AnError
	}

	m, _ := createPooledDispatcherWithFactory(factory)

	_, err := m.Send(context.Background(), "data")
	assert.ErrorIs(t, err, assert.AnError)
}

func TestPooledDispatcher_Send_FailsToAcquireSupervisorStartFails(t *testing.T) {
	m, sv, _ := createPooledDispatcher(t)

	sv.EXPECT().Start(mock.Anything).Return(assert.AnError)

	_, err := m.Send(context.Background(), "data")
	assert.ErrorIs(t, err, assert.AnError)
}

func TestPooledDispatcher_Send_Fails(t *testing.T) {
	m, sv, _ := createPooledDispatcher(t)

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Shutdown(mock.Anything).Return(nil, nil)
	sv.EXPECT().Send(mock.Anything, "data").Return(nil, assert.AnError)

	_, err := m.Send(context.Background(), "data")
	assert.ErrorIs(t, err, assert.AnError)

	sv.AssertCalled(t, "Start", mock.Anything)

	// wait for the background shutdown goroutines to finish
	m.Shutdown(context.Background())
}

func TestPooledDispatcher_Send_ReleaseSupervisorWait(t *testing.T) {
	m, sv, _ := createPooledDispatcher(t)

	var waited bool

	wait := func() error {
		waited = true
		return nil
	}

	result := &supervisor.Result[any]{
		Data:    "data",
		Release: wait,
	}

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Shutdown(mock.Anything).Return(nil, nil)
	sv.EXPECT().Send(mock.Anything, "data").Return(result, nil)

	_, err := m.Send(context.Background(), "data")
	assert.NoError(t, err)

	// wait for the release to happen in the background goroutine
	m.Shutdown(context.Background())

	assert.True(t, waited)
}

func TestPooledDispatcher_Send_ReleaseSupervisorWaitError(t *testing.T) {
	m, sv, _ := createPooledDispatcher(t)

	var waited bool

	wait := func() error {
		waited = true
		return assert.AnError
	}

	result := &supervisor.Result[any]{
		Data:    "data",
		Release: wait,
	}

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Shutdown(mock.Anything).Return(nil, nil)
	sv.EXPECT().Send(mock.Anything, "data").Return(result, nil)

	_, err := m.Send(context.Background(), "data")
	assert.NoError(t, err)

	// wait for the release to happen in the background goroutine
	m.Shutdown(context.Background())

	assert.True(t, waited)
}

func TestPooledDispatcher_Send_ReleaseSupervisorWaitErrorOnDestroy(t *testing.T) {
	m, sv, _ := createPooledDispatcher(t)

	var waited int

	wait := func() error {
		waited++
		return assert.AnError
	}

	result := &supervisor.Result[any]{
		Data:    "data",
		Release: wait,
	}

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Shutdown(mock.Anything).Return(wait, nil)
	sv.EXPECT().Send(mock.Anything, "data").Return(result, nil)

	_, err := m.Send(context.Background(), "data")
	assert.NoError(t, err)

	// wait for the release to happen in a goroutine
	<-time.After(1 * time.Millisecond)

	assert.Equal(t, 2, waited)
}

func TestPooledDispatcher_Send_ReleaseSupervisorWaitErrorShutdown(t *testing.T) {
	m, sv, _ := createPooledDispatcher(t)

	var waited int

	wait := func() error {
		waited++
		return assert.AnError
	}

	result := &supervisor.Result[any]{
		Data:    "data",
		Release: wait,
	}

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Shutdown(mock.Anything).Return(wait, assert.AnError)
	sv.EXPECT().Send(mock.Anything, "data").Return(result, nil)

	_, err := m.Send(context.Background(), "data")
	assert.NoError(t, err)

	// wait for the release to happen in a goroutine
	<-time.After(1 * time.Millisecond)

	assert.Equal(t, 1, waited)
}

func TestPooledDispatcher_Shutdown_DestroysSupervisor(t *testing.T) {
	m, sv, _ := createPooledDispatcher(t)

	result := &supervisor.Result[any]{
		Data: "data",
	}

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Shutdown(mock.Anything).Return(nil, nil)
	sv.EXPECT().Send(mock.Anything, "data").Return(result, nil)

	_, err := m.Send(context.Background(), "data")
	assert.NoError(t, err)

	m.Shutdown(context.Background())
}

// MARK: - helpers

func createPooledDispatcher(t *testing.T) (dispatcher.Dispatcher[any, any], *supervisor.MockSupervisor[any, any], error) {
	sv := supervisor.NewMockSupervisor[any, any](t)

	factory := func(params supervisor.Params[any, any]) (supervisor.Supervisor[any, any], error) {
		return sv, nil
	}

	m, err := createPooledDispatcherWithFactory(factory)
	if err != nil {
		return nil, nil, err
	}

	return m, sv, nil
}

func createPooledDispatcherWithFactory(
	factory dispatcher.SupervisorFactory[any, any],
) (dispatcher.Dispatcher[any, any], error) {
	return dispatcher.NewPooledDispatcher(dispatcher.PooledDispatcherParams[any, any]{
		Config: dispatcher.PooledDispatcherConfig[any, any]{
			MaxWorkers: 1,
		},
		Context:           context.Background(),
		SupervisorFactory: factory,
		Log:               zap.NewNop(),
	})
}

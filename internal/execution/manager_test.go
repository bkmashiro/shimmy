package execution_test

import (
	"context"
	"testing"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution"
	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

func TestManager_New_FailsInvalidCapacity(t *testing.T) {
	m, err := execution.NewManager(execution.Params[any, any]{
		Config: execution.Config[any, any]{
			MaxCapacity: 0,
		},
		Context: context.Background(),
		Log:     zap.NewNop(),
	})
	assert.Error(t, err)
	assert.Nil(t, m)
}

func TestManager_New_CreatesNewManager(t *testing.T) {
	m, _, err := createManager(t)
	assert.NoError(t, err)
	assert.NotNil(t, m)
}

func TestManager_Send(t *testing.T) {
	m, sv, _ := createManager(t)

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Send(mock.Anything, "data").Return(&supervisor.Result[any]{Data: "data"}, nil)

	_, err := m.Send(context.Background(), "data")
	assert.NoError(t, err)
}

func TestManager_Send_FailsToAcquireSupervisor(t *testing.T) {
	factory := func(params supervisor.Params[any, any]) (supervisor.Supervisor[any, any], error) {
		return nil, assert.AnError
	}

	m, _ := createManagerWithFactory(factory)

	_, err := m.Send(context.Background(), "data")
	assert.ErrorIs(t, err, assert.AnError)
}

func TestManager_Send_FailsToAcquireSupervisorStartFails(t *testing.T) {
	m, sv, _ := createManager(t)

	sv.EXPECT().Start(mock.Anything).Return(assert.AnError)

	_, err := m.Send(context.Background(), "data")
	assert.ErrorIs(t, err, assert.AnError)
}

func TestManager_Send_Fails(t *testing.T) {
	m, sv, _ := createManager(t)

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Send(mock.Anything, "data").Return(nil, assert.AnError)

	_, err := m.Send(context.Background(), "data")
	assert.ErrorIs(t, err, assert.AnError)

	sv.AssertCalled(t, "Start", mock.Anything)
}

func TestManager_Send_ReleaseSupervisorWait(t *testing.T) {
	m, sv, _ := createManager(t)

	var waited bool

	wait := func() error {
		waited = true
		return nil
	}

	result := &supervisor.Result[any]{
		Data: "data",
		Wait: wait,
	}

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Send(mock.Anything, "data").Return(result, nil)

	_, err := m.Send(context.Background(), "data")
	assert.NoError(t, err)

	// wait for the release to happen in a goroutine
	<-time.After(1 * time.Millisecond)

	assert.True(t, waited)
}

func TestManager_Send_ReleaseSupervisorWaitError(t *testing.T) {
	m, sv, _ := createManager(t)

	var waited bool

	wait := func() error {
		waited = true
		return assert.AnError
	}

	result := &supervisor.Result[any]{
		Data: "data",
		Wait: wait,
	}

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Send(mock.Anything, "data").Return(result, nil)
	sv.EXPECT().Shutdown(mock.Anything).Return(nil, nil)

	_, err := m.Send(context.Background(), "data")
	assert.NoError(t, err)

	// wait for the release to happen in a goroutine
	<-time.After(1 * time.Millisecond)

	assert.True(t, waited)
}

func TestManager_Send_ReleaseSupervisorWaitErrorOnDestroy(t *testing.T) {
	m, sv, _ := createManager(t)

	var waited int

	wait := func() error {
		waited++
		return assert.AnError
	}

	result := &supervisor.Result[any]{
		Data: "data",
		Wait: wait,
	}

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Send(mock.Anything, "data").Return(result, nil)
	sv.EXPECT().Shutdown(mock.Anything).Return(wait, nil)

	_, err := m.Send(context.Background(), "data")
	assert.NoError(t, err)

	// wait for the release to happen in a goroutine
	<-time.After(1 * time.Millisecond)

	assert.Equal(t, 2, waited)
}

func TestManager_Send_ReleaseSupervisorWaitErrorShutdown(t *testing.T) {
	m, sv, _ := createManager(t)

	var waited int

	wait := func() error {
		waited++
		return assert.AnError
	}

	result := &supervisor.Result[any]{
		Data: "data",
		Wait: wait,
	}

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Send(mock.Anything, "data").Return(result, nil)
	sv.EXPECT().Shutdown(mock.Anything).Return(wait, assert.AnError)

	_, err := m.Send(context.Background(), "data")
	assert.NoError(t, err)

	// wait for the release to happen in a goroutine
	<-time.After(1 * time.Millisecond)

	assert.Equal(t, 1, waited)
}

func TestManager_Shutdown_DestroysSupervisor(t *testing.T) {
	m, sv, _ := createManager(t)

	result := &supervisor.Result[any]{
		Data: "data",
	}

	sv.EXPECT().Start(mock.Anything).Return(nil)
	sv.EXPECT().Send(mock.Anything, "data").Return(result, nil)
	sv.EXPECT().Shutdown(mock.Anything).Return(nil, nil)

	_, err := m.Send(context.Background(), "data")
	assert.NoError(t, err)

	m.Shutdown()
}

// MARK: - helpers

func createManager(t *testing.T) (*execution.WorkerManager[any, any], *supervisor.MockSupervisor[any, any], error) {
	sv := supervisor.NewMockSupervisor[any, any](t)

	factory := func(params supervisor.Params[any, any]) (supervisor.Supervisor[any, any], error) {
		return sv, nil
	}

	m, err := createManagerWithFactory(factory)
	if err != nil {
		return nil, nil, err
	}

	return m, sv, nil
}

func createManagerWithFactory(
	factory execution.SupervisorFactory[any, any],
) (*execution.WorkerManager[any, any], error) {
	return execution.NewManager(execution.Params[any, any]{
		Config: execution.Config[any, any]{
			MaxCapacity: 1,
		},
		Context:           context.Background(),
		SupervisorFactory: factory,
		Log:               zap.NewNop(),
	})
}

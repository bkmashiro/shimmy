package supervisor_test

import (
	"context"
	"testing"

	supervisor_mocks "github.com/lambda-feedback/shimmy/mocks/supervisor"
	"github.com/lambda-feedback/shimmy/supervisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

func TestSupervisor_New_PersistentFileIO_Fails(t *testing.T) {
	_, _, err := createSupervisor(t, true, supervisor.FileIO)

	assert.ErrorIs(t, err, supervisor.ErrInvalidPersistentFileIO)
}

func TestSupervisor_New_DefaultWorkerFactory(t *testing.T) {
	s, err := supervisor.New(supervisor.SupervisorConfig[any, any]{
		Persistent:    false,
		Mode:          supervisor.StdIO,
		WorkerFactory: nil,
		Log:           zap.NewNop(),
	})
	assert.NoError(t, err)
	assert.NotNil(t, s)

	err = s.Start(context.Background())
	assert.NoError(t, err)
}

func TestSupervisor_Start_FailsToAcquireWorker(t *testing.T) {
	mockFactory := func(i supervisor.IOMode, l *zap.Logger) (supervisor.Adapter[any, any], error) {
		return nil, assert.AnError
	}

	s, err := createSupervisorWithFactory(true, supervisor.StdIO, mockFactory)
	assert.NoError(t, err)

	err = s.Start(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

func TestSupervisor_Start_Transient_DoesNotAcquireWorker(t *testing.T) {
	var called bool

	mockFactory := func(i supervisor.IOMode, l *zap.Logger) (supervisor.Adapter[any, any], error) {
		called = true
		return nil, nil
	}

	s, err := createSupervisorWithFactory(false, supervisor.StdIO, mockFactory)
	assert.NoError(t, err)

	err = s.Start(context.Background())
	assert.NoError(t, err)
	assert.False(t, called)
}

func TestSupervisor_Start_Persistent_AcquiresWorker(t *testing.T) {
	var called bool

	a := supervisor_mocks.NewMockAdapter[any, any](t)
	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)

	mockFactory := func(i supervisor.IOMode, l *zap.Logger) (supervisor.Adapter[any, any], error) {
		called = true
		return a, nil
	}

	s, err := createSupervisorWithFactory(true, supervisor.StdIO, mockFactory)
	assert.NoError(t, err)

	err = s.Start(context.Background())
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestSupervisor_Start_Persistent_StartsWorker(t *testing.T) {
	s, a, err := createSupervisor(t, true, supervisor.StdIO)
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)

	err = s.Start(context.Background())
	assert.NoError(t, err)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)
}

func TestSupervisor_Start_Fails(t *testing.T) {
	s, a, err := createSupervisor(t, true, supervisor.StdIO)
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(assert.AnError)

	err = s.Start(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

func TestSupervisor_Suspend_Idle_DoesNothing(t *testing.T) {
	s, a, err := createSupervisor(t, false, supervisor.StdIO)
	assert.NoError(t, err)

	_, err = s.Suspend(context.Background())
	assert.NoError(t, err)

	a.AssertNotCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSupervisor_Suspend_Transient_StopsWorker(t *testing.T) {
	s, a, err := createSupervisor(t, false, supervisor.StdIO)
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop(mock.Anything, mock.Anything).Return(nil, nil)
	a.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).Return("result", nil)

	_, _ = s.Send(context.Background(), nil)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)

	_, err = s.Suspend(context.Background())
	assert.NoError(t, err)

	a.AssertCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSupervisor_Suspend_Persistent_DoesNotStopWorker(t *testing.T) {
	s, a, err := createSupervisor(t, true, supervisor.StdIO)
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).Return("result", nil)

	_, _ = s.Send(context.Background(), nil)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)

	_, err = s.Suspend(context.Background())
	assert.NoError(t, err)

	a.AssertNotCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSupervisor_Shutdown_Transient_StopsWorker(t *testing.T) {
	s, a, err := createSupervisor(t, false, supervisor.StdIO)
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop(mock.Anything, mock.Anything).Return(nil, nil)
	a.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).Return("result", nil)

	_, _ = s.Send(context.Background(), nil)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)

	_, err = s.Shutdown(context.Background())
	assert.NoError(t, err)

	a.AssertCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSupervisor_Shutdown_Persistent_StopsWorker(t *testing.T) {
	s, a, err := createSupervisor(t, true, supervisor.StdIO)
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop(mock.Anything, mock.Anything).Return(nil, nil)
	a.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).Return("result", nil)

	_, _ = s.Send(context.Background(), nil)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)

	_, err = s.Shutdown(context.Background())
	assert.NoError(t, err)

	a.AssertCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSupervisor_Send_Persistent_ReusesWorker(t *testing.T) {
	s, a, err := createSupervisor(t, true, supervisor.StdIO)
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).Return("result", nil)

	_, _ = s.Send(context.Background(), nil)

	_, _ = s.Send(context.Background(), nil)

	a.AssertNumberOfCalls(t, "Start", 1)
}

func TestSupervisor_Send_Transient_DoesNotReuseWorker(t *testing.T) {
	s, a, err := createSupervisor(t, false, supervisor.StdIO)
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop(mock.Anything, mock.Anything).Return(nil, nil)
	a.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).Return("result", nil)

	// boots first, transient worker
	_, _ = s.Send(context.Background(), nil)

	// boots second, transient worker
	_, _ = s.Send(context.Background(), nil)

	a.AssertNumberOfCalls(t, "Start", 2)
	a.AssertNumberOfCalls(t, "Stop", 2)
}

func TestSupervisor_Send_SendsData(t *testing.T) {
	s, a, err := createSupervisor(t, true, supervisor.StdIO)
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).Return("result", nil)

	res, err := s.Send(context.Background(), "data")
	assert.NoError(t, err)
	assert.Equal(t, res.Data, "result")

	a.AssertCalled(t, "Send", mock.Anything, "data", mock.Anything)
}

func TestSupervisor_Send_FailsToAcquireWorker(t *testing.T) {
	mockFactory := func(i supervisor.IOMode, l *zap.Logger) (supervisor.Adapter[any, any], error) {
		return nil, assert.AnError
	}

	s, err := createSupervisorWithFactory(true, supervisor.StdIO, mockFactory)
	assert.NoError(t, err)

	res, err := s.Send(context.Background(), "data")
	assert.ErrorIs(t, err, assert.AnError)
	assert.Nil(t, res)
}

func TestSupervisor_Send_FailsToReleaseWorker(t *testing.T) {
	s, a, err := createSupervisor(t, false, supervisor.StdIO)
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop(mock.Anything, mock.Anything).Return(nil, assert.AnError)
	a.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).Return("result", nil)

	res, err := s.Send(context.Background(), "data")
	assert.NoError(t, err)
	assert.Equal(t, res.Data, "result")

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)
	a.AssertCalled(t, "Send", mock.Anything, "data", mock.Anything)
	a.AssertCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSupervisor_Send_Fails(t *testing.T) {
	s, a, err := createSupervisor(t, true, supervisor.StdIO)
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).Return("result", assert.AnError)

	res, err := s.Send(context.Background(), "data")
	assert.ErrorIs(t, err, assert.AnError)
	assert.Nil(t, res)
}

// MARK: - mocks

func createSupervisor(t *testing.T, persistent bool, mode supervisor.IOMode) (
	supervisor.Supervisor[any, any],
	*supervisor_mocks.MockAdapter[any, any],
	error,
) {
	adapter := supervisor_mocks.NewMockAdapter[any, any](t)

	workerFactory := func(mode supervisor.IOMode, log *zap.Logger) (supervisor.Adapter[any, any], error) {
		return adapter, nil
	}

	s, err := createSupervisorWithFactory(persistent, mode, workerFactory)
	if err != nil {
		return nil, nil, err
	}

	return s, adapter, nil
}

func createSupervisorWithFactory(
	persistent bool,
	mode supervisor.IOMode,
	factory supervisor.AdapterFactoryFn[any, any],
) (supervisor.Supervisor[any, any], error) {
	return supervisor.New(supervisor.SupervisorConfig[any, any]{
		Persistent:    persistent,
		Mode:          mode,
		WorkerFactory: factory,
		Log:           zap.NewNop(),
	})
}

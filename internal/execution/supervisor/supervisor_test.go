package supervisor_test

import (
	"context"
	"testing"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

func TestSupervisor_New_DefaultWorkerFactory(t *testing.T) {
	s, err := supervisor.New(supervisor.Params{
		Config: supervisor.Config{
			IO: supervisor.IOConfig{Interface: supervisor.FileIO},
		},
		WorkerFactory: nil,
		Log:           zap.NewNop(),
	})
	assert.NoError(t, err)
	assert.NotNil(t, s)

	err = s.Start(context.Background())
	assert.NoError(t, err)
}

func TestSupervisor_Start_FailsToAcquireWorker(t *testing.T) {
	mockFactory := func(supervisor.AdapterWorkerFactoryFn, supervisor.IOConfig, *zap.Logger) (supervisor.Adapter, error) {
		return nil, assert.AnError
	}

	s, err := createSupervisorWithFactory(supervisor.RpcIO, mockFactory)
	assert.NoError(t, err)

	err = s.Start(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

func TestSupervisor_Start_Transient_DoesNotAcquireWorker(t *testing.T) {
	var called bool

	mockFactory := func(supervisor.AdapterWorkerFactoryFn, supervisor.IOConfig, *zap.Logger) (supervisor.Adapter, error) {
		called = true
		return nil, nil
	}

	s, err := createSupervisorWithFactory(supervisor.FileIO, mockFactory)
	assert.NoError(t, err)

	err = s.Start(context.Background())
	assert.NoError(t, err)
	assert.False(t, called)
}

func TestSupervisor_Start_Persistent_AcquiresWorker(t *testing.T) {
	var called bool

	a := supervisor.NewMockAdapter(t)
	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)

	mockFactory := func(supervisor.AdapterWorkerFactoryFn, supervisor.IOConfig, *zap.Logger) (supervisor.Adapter, error) {
		called = true
		return a, nil
	}

	s, err := createSupervisorWithFactory(supervisor.RpcIO, mockFactory)
	assert.NoError(t, err)

	err = s.Start(context.Background())
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestSupervisor_Start_Persistent_StartsWorker(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.RpcIO)
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)

	err = s.Start(context.Background())
	assert.NoError(t, err)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)
}

func TestSupervisor_Start_Fails(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.RpcIO)
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(assert.AnError)

	err = s.Start(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

func TestSupervisor_Suspend_Idle_DoesNothing(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.RpcIO)
	assert.NoError(t, err)

	_, err = s.Suspend(context.Background())
	assert.NoError(t, err)

	a.AssertNotCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSupervisor_Suspend_Transient_StopsWorker(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.FileIO)
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop(mock.Anything).Return(nil, nil)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(nil, nil)

	_, _ = s.Send(context.Background(), "test", data)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)

	_, err = s.Suspend(context.Background())
	assert.NoError(t, err)

	a.AssertCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSupervisor_Suspend_Persistent_DoesNotStopWorker(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.RpcIO)
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(nil, nil)

	_, _ = s.Send(context.Background(), "test", data)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)

	_, err = s.Suspend(context.Background())
	assert.NoError(t, err)

	a.AssertNotCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSupervisor_Shutdown_Transient_StopsWorker(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.FileIO)
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop(mock.Anything).Return(nil, nil)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(nil, nil)

	_, _ = s.Send(context.Background(), "test", data)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)

	_, err = s.Shutdown(context.Background())
	assert.NoError(t, err)

	a.AssertCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSupervisor_Shutdown_Persistent_StopsWorker(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.RpcIO)
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop(mock.Anything).Return(nil, nil)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(nil, nil)

	_, _ = s.Send(context.Background(), "test", data)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)

	_, err = s.Shutdown(context.Background())
	assert.NoError(t, err)

	a.AssertCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSupervisor_Send_Persistent_ReusesWorker(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.RpcIO)
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(nil, nil)

	_, _ = s.Send(context.Background(), "test", data)

	_, _ = s.Send(context.Background(), "test", data)

	a.AssertNumberOfCalls(t, "Start", 1)
}

func TestSupervisor_Send_Transient_DoesNotReuseWorker(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.FileIO)
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop(mock.Anything).Return(nil, nil)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(nil, nil)

	// boots first, transient worker
	_, _ = s.Send(context.Background(), "test", data)

	// boots second, transient worker
	_, _ = s.Send(context.Background(), "test", data)

	a.AssertNumberOfCalls(t, "Start", 2)
	a.AssertNumberOfCalls(t, "Stop", 2)
}

func TestSupervisor_Send_SendsData(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.RpcIO)
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}
	resData := map[string]any{"result": "result"}

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(resData, nil)

	res, err := s.Send(context.Background(), "test", data)
	assert.NoError(t, err)
	assert.Equal(t, resData, res.Data)
}

func TestSupervisor_Send_FailsToAcquireWorker(t *testing.T) {
	mockFactory := func(supervisor.AdapterWorkerFactoryFn, supervisor.IOConfig, *zap.Logger) (supervisor.Adapter, error) {
		return nil, assert.AnError
	}

	s, err := createSupervisorWithFactory(supervisor.RpcIO, mockFactory)
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}

	res, err := s.Send(context.Background(), "test", data)
	assert.ErrorIs(t, err, assert.AnError)
	assert.Nil(t, res)
}

func TestSupervisor_Send_FailsToReleaseWorker(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.FileIO)
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}
	resData := map[string]any{"result": "result"}

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop(mock.Anything).Return(nil, assert.AnError)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(resData, nil)

	res, err := s.Send(context.Background(), "test", data)
	assert.NoError(t, err)
	assert.Equal(t, resData, res.Data)
}

func TestSupervisor_Send_Fails(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.RpcIO)
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(nil, assert.AnError)

	res, err := s.Send(context.Background(), "test", data)
	assert.ErrorIs(t, err, assert.AnError)
	assert.NotNil(t, res)
}

// MARK: - mocks

func createSupervisor(t *testing.T, mode supervisor.IOInterface) (
	supervisor.Supervisor,
	*supervisor.MockAdapter,
	error,
) {
	adapter := supervisor.NewMockAdapter(t)

	adapterFactory := func(supervisor.AdapterWorkerFactoryFn, supervisor.IOConfig, *zap.Logger) (supervisor.Adapter, error) {
		return adapter, nil
	}

	s, err := createSupervisorWithFactory(mode, adapterFactory)
	if err != nil {
		return nil, nil, err
	}

	return s, adapter, nil
}

func createSupervisorWithFactory(
	mode supervisor.IOInterface,
	factory supervisor.AdapterFactoryFn,
) (supervisor.Supervisor, error) {
	return supervisor.New(supervisor.Params{
		Config: supervisor.Config{
			IO: supervisor.IOConfig{Interface: mode},
		},
		AdapterFactory: factory,
		Log:            zap.NewNop(),
	})
}

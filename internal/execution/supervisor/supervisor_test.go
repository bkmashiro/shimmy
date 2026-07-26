package supervisor_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
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

func TestSupervisor_Start_RPCInvocationLifecycleDoesNotAcquireWorker(t *testing.T) {
	var called bool
	mockFactory := func(supervisor.AdapterWorkerFactoryFn, supervisor.IOConfig, *zap.Logger) (supervisor.Adapter, error) {
		called = true
		return nil, nil
	}

	s, err := createSupervisorWithConfig(supervisor.Config{
		IO:              supervisor.IOConfig{Interface: supervisor.RpcIO},
		WorkerLifecycle: supervisor.WorkerLifecycleInvocation,
	}, mockFactory)
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

func TestSupervisor_Start_RPCEagerPrebootsCleanWorker(t *testing.T) {
	s, a, err := createSupervisorWithAdapterConfig(t, supervisor.Config{
		IO:              supervisor.IOConfig{Interface: supervisor.RpcIO},
		WorkerLifecycle: supervisor.WorkerLifecycleEager,
	})
	assert.NoError(t, err)

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil).Once()
	assert.NoError(t, s.Start(context.Background()))
	a.AssertNumberOfCalls(t, "Start", 1)
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
	a.EXPECT().Stop().Return(nil, nil)
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
	a.EXPECT().Stop().Return(nil, nil)
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
	a.EXPECT().Stop().Return(nil, nil)
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
	a.EXPECT().Stop().Return(nil, nil)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(nil, nil)

	// boots first, transient worker
	_, _ = s.Send(context.Background(), "test", data)

	// boots second, transient worker
	_, _ = s.Send(context.Background(), "test", data)

	a.AssertNumberOfCalls(t, "Start", 2)
	a.AssertNumberOfCalls(t, "Stop", 2)
}

func TestSupervisor_Send_RPCInvocationLifecycleDoesNotReuseWorker(t *testing.T) {
	s, a, err := createSupervisorWithAdapterConfig(t, supervisor.Config{
		IO:              supervisor.IOConfig{Interface: supervisor.RpcIO},
		WorkerLifecycle: supervisor.WorkerLifecycleInvocation,
	})
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}
	waited := 0
	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop().Return(supervisor.ReleaseFunc(func(context.Context) error {
		waited++
		return nil
	}), nil)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(nil, nil)

	for index := range 2 {
		res, sendErr := s.Send(context.Background(), "test", data)
		assert.NoError(t, sendErr)
		assert.Equal(t, index+1, waited, "worker exit must be awaited before Send returns")
		assert.NoError(t, res.Release(context.Background()))
	}

	a.AssertNumberOfCalls(t, "Start", 2)
	a.AssertNumberOfCalls(t, "Stop", 2)
}

func TestSupervisor_Send_RPCInvocationLifecycleStopsAfterSendFailure(t *testing.T) {
	s, a, err := createSupervisorWithAdapterConfig(t, supervisor.Config{
		IO:              supervisor.IOConfig{Interface: supervisor.RpcIO},
		WorkerLifecycle: supervisor.WorkerLifecycleInvocation,
	})
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}
	waited := false
	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop().Return(supervisor.ReleaseFunc(func(context.Context) error {
		waited = true
		return nil
	}), nil)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(nil, assert.AnError)

	res, sendErr := s.Send(context.Background(), "test", data)
	assert.ErrorIs(t, sendErr, assert.AnError)
	assert.NotNil(t, res)
	assert.True(t, waited, "failed worker exit must be awaited before Send returns")
	assert.NoError(t, res.Release(context.Background()))
	a.AssertNumberOfCalls(t, "Stop", 1)
}

func TestSupervisor_Send_RPCInvocationLifecycleHoldsSlotUntilWorkerExit(t *testing.T) {
	s, a, err := createSupervisorWithAdapterConfig(t, supervisor.Config{
		IO:              supervisor.IOConfig{Interface: supervisor.RpcIO},
		WorkerLifecycle: supervisor.WorkerLifecycleInvocation,
		StopParams:      supervisor.StopConfig{Timeout: time.Second},
	})
	assert.NoError(t, err)

	waitStarted := make(chan struct{})
	allowExit := make(chan struct{})
	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil).Once()
	a.EXPECT().Send(mock.Anything, "test", mock.Anything, mock.Anything).Return(nil, nil).Once()
	a.EXPECT().Stop().Return(supervisor.ReleaseFunc(func(context.Context) error {
		close(waitStarted)
		<-allowExit
		return nil
	}), nil).Once()

	returned := make(chan struct{})
	go func() {
		_, _ = s.Send(context.Background(), "test", map[string]any{})
		close(returned)
	}()

	<-waitStarted
	select {
	case <-returned:
		t.Fatal("Send returned before the old worker exited")
	default:
	}
	close(allowExit)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("Send did not return after the old worker exited")
	}
}

func TestSupervisor_Send_RPCEagerReleaseRefillsWithoutBlockingAndNextSendWaits(t *testing.T) {
	s, a, err := createSupervisorWithAdapterConfig(t, supervisor.Config{
		IO:              supervisor.IOConfig{Interface: supervisor.RpcIO},
		WorkerLifecycle: supervisor.WorkerLifecycleEager,
		StopParams:      supervisor.StopConfig{Timeout: time.Second},
	})
	assert.NoError(t, err)

	stopStarted := make(chan struct{})
	allowOldExit := make(chan struct{})
	thirdStarted := make(chan struct{})
	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil).Once()
	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil).Once()
	a.EXPECT().Start(mock.Anything, mock.Anything).Run(func(context.Context, supervisor.StartConfig) {
		close(thirdStarted)
	}).Return(nil).Once()
	a.EXPECT().Send(mock.Anything, "test", mock.Anything, mock.Anything).Return(map[string]any{"ok": true}, nil).Twice()
	a.EXPECT().Stop().Return(supervisor.ReleaseFunc(func(context.Context) error {
		close(stopStarted)
		<-allowOldExit
		return nil
	}), nil).Once()
	a.EXPECT().Stop().Return(supervisor.ReleaseFunc(func(context.Context) error { return nil }), nil).Twice()

	assert.NoError(t, s.Start(context.Background()))
	first, sendErr := s.Send(context.Background(), "test", map[string]any{"sequence": 1})
	assert.NoError(t, sendErr)
	select {
	case <-stopStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("eager Send did not detach the served worker before releasing its send lock")
	}
	assert.NoError(t, first.Release(context.Background()))

	secondReturned := make(chan *supervisor.Result, 1)
	secondFailed := make(chan error, 1)
	go func() {
		result, err := s.Send(context.Background(), "test", map[string]any{"sequence": 2})
		if err != nil {
			secondFailed <- err
			return
		}
		secondReturned <- result
	}()
	select {
	case <-secondReturned:
		t.Fatal("next eager Send used a worker before the old worker exited and refill completed")
	case err := <-secondFailed:
		t.Fatalf("next eager Send failed before refill: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(allowOldExit)
	var second *supervisor.Result
	select {
	case second = <-secondReturned:
	case err := <-secondFailed:
		t.Fatalf("next eager Send failed after refill: %v", err)
	case <-time.After(time.Second):
		t.Fatal("next eager Send did not use the refilled worker")
	}
	assert.NotNil(t, second)
	select {
	case <-thirdStarted:
	case <-time.After(time.Second):
		t.Fatal("eager did not begin the next clean refill after the second request")
	}
	a.AssertNumberOfCalls(t, "Start", 3)

	wait, shutdownErr := s.Shutdown(context.Background())
	assert.NoError(t, shutdownErr)
	assert.NoError(t, wait())
}

func TestSupervisor_Shutdown_RPCEagerWaitsForInFlightSend(t *testing.T) {
	config := supervisor.Config{
		IO:              supervisor.IOConfig{Interface: supervisor.RpcIO},
		WorkerLifecycle: supervisor.WorkerLifecycleEager,
		StopParams:      supervisor.StopConfig{Timeout: time.Second},
	}
	s, a, err := createSupervisorWithAdapterConfig(t, config)
	assert.NoError(t, err)
	sendStarted := make(chan struct{})
	allowSend := make(chan struct{})
	stopCalled := make(chan struct{})
	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil).Once()
	a.EXPECT().Send(mock.Anything, "test", mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, string, map[string]any, time.Duration) (map[string]any, error) {
			close(sendStarted)
			<-allowSend
			return map[string]any{"ok": true}, nil
		},
	).Once()
	a.EXPECT().Stop().RunAndReturn(func() (supervisor.ReleaseFunc, error) {
		close(stopCalled)
		return func(context.Context) error { return nil }, nil
	}).Once()

	assert.NoError(t, s.Start(context.Background()))
	sendDone := make(chan error, 1)
	go func() {
		result, sendErr := s.Send(context.Background(), "test", map[string]any{"sequence": 1})
		if sendErr == nil {
			sendErr = result.Release(context.Background())
		}
		sendDone <- sendErr
	}()
	<-sendStarted

	shutdownDone := make(chan error, 1)
	go func() {
		wait, shutdownErr := s.Shutdown(context.Background())
		if shutdownErr == nil {
			shutdownErr = wait()
		}
		shutdownDone <- shutdownErr
	}()
	select {
	case <-stopCalled:
		t.Fatal("eager Shutdown stopped a worker while Send was in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(allowSend)
	assert.NoError(t, <-sendDone)
	select {
	case shutdownErr := <-shutdownDone:
		assert.NoError(t, shutdownErr)
	case <-time.After(time.Second):
		t.Fatal("eager Shutdown did not complete after Send released the lock")
	}
}

func TestSupervisor_Suspend_RPCEagerFailsClosed(t *testing.T) {
	s, _, err := createSupervisorWithAdapterConfig(t, supervisor.Config{
		IO:              supervisor.IOConfig{Interface: supervisor.RpcIO},
		WorkerLifecycle: supervisor.WorkerLifecycleEager,
	})
	assert.NoError(t, err)
	_, err = s.Suspend(context.Background())
	assert.ErrorContains(t, err, "cannot be suspended")
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
	a.EXPECT().Stop().Return(nil, assert.AnError)
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

func createSupervisorWithAdapterConfig(
	t *testing.T,
	config supervisor.Config,
) (supervisor.Supervisor, *supervisor.MockAdapter, error) {
	adapter := supervisor.NewMockAdapter(t)
	factory := func(supervisor.AdapterWorkerFactoryFn, supervisor.IOConfig, *zap.Logger) (supervisor.Adapter, error) {
		return adapter, nil
	}
	s, err := createSupervisorWithConfig(config, factory)
	return s, adapter, err
}

func createSupervisorWithFactory(
	mode supervisor.IOInterface,
	factory supervisor.AdapterFactoryFn,
) (supervisor.Supervisor, error) {
	return createSupervisorWithConfig(supervisor.Config{
		IO: supervisor.IOConfig{Interface: mode},
	}, factory)
}

func createSupervisorWithConfig(
	config supervisor.Config,
	factory supervisor.AdapterFactoryFn,
) (supervisor.Supervisor, error) {
	return supervisor.New(supervisor.Params{
		Config:         config,
		Context:        context.Background(),
		AdapterFactory: factory,
		Log:            zap.NewNop(),
	})
}

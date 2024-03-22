package supervisor_test

import (
	"context"
	"testing"

	"github.com/lambda-feedback/shimmy/supervisor"
	"github.com/lambda-feedback/shimmy/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

func TestNew_PersistentFileIO_Fails(t *testing.T) {
	_, _, err := createSupervisor(true, supervisor.FileIO)

	assert.ErrorIs(t, err, supervisor.ErrInvalidPersistentFileIO)
}

func TestStart_Transient_DoesNotAcquireWorker(t *testing.T) {
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

func TestStart_Persistent_AcquiresWorker(t *testing.T) {
	var called bool

	a := &mockAdapter{}
	a.On("Start", mock.Anything, mock.Anything).Return(nil)

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

func TestStart_Persistent_StartsWorker(t *testing.T) {
	s, a, err := createSupervisor(true, supervisor.StdIO)
	assert.NoError(t, err)

	a.On("Start", mock.Anything, mock.Anything).Return(nil)

	err = s.Start(context.Background())
	assert.NoError(t, err)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)
}

func TestSuspend_Idle_DoesNothing(t *testing.T) {
	s, a, err := createSupervisor(false, supervisor.StdIO)
	assert.NoError(t, err)

	a.On("Stop", mock.Anything, mock.Anything).Return(nil)

	_, err = s.Suspend(context.Background())
	assert.NoError(t, err)

	a.AssertNotCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSuspend_Transient_StopsWorker(t *testing.T) {
	s, a, err := createSupervisor(false, supervisor.StdIO)
	assert.NoError(t, err)

	a.On("Start", mock.Anything, mock.Anything).Return(nil)
	a.On("Stop", mock.Anything, mock.Anything).Return(nil, nil)
	a.On("Send", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	_, _ = s.Send(context.Background(), nil)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)

	_, err = s.Suspend(context.Background())
	assert.NoError(t, err)

	a.AssertCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSuspend_Persistent_DoesNotStopWorker(t *testing.T) {
	s, a, err := createSupervisor(true, supervisor.StdIO)
	assert.NoError(t, err)

	a.On("Start", mock.Anything, mock.Anything).Return(nil)
	a.On("Stop", mock.Anything, mock.Anything).Return(nil)
	a.On("Send", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	_, _ = s.Send(context.Background(), nil)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)

	_, err = s.Suspend(context.Background())
	assert.NoError(t, err)

	a.AssertNotCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestShutdown_Transient_StopsWorker(t *testing.T) {
	s, a, err := createSupervisor(false, supervisor.StdIO)
	assert.NoError(t, err)

	a.On("Start", mock.Anything, mock.Anything).Return(nil)
	a.On("Stop", mock.Anything, mock.Anything).Return(nil, nil)
	a.On("Send", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	_, _ = s.Send(context.Background(), nil)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)

	_, err = s.Shutdown(context.Background())
	assert.NoError(t, err)

	a.AssertCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestShutdown_Persistent_StopsWorker(t *testing.T) {
	s, a, err := createSupervisor(true, supervisor.StdIO)
	assert.NoError(t, err)

	a.On("Start", mock.Anything, mock.Anything).Return(nil)
	a.On("Stop", mock.Anything, mock.Anything).Return(nil, nil)
	a.On("Send", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	_, _ = s.Send(context.Background(), nil)

	a.AssertCalled(t, "Start", mock.Anything, mock.Anything)

	_, err = s.Shutdown(context.Background())
	assert.NoError(t, err)

	a.AssertCalled(t, "Stop", mock.Anything, mock.Anything)
}

func TestSend_Persistent_ReusesWorker(t *testing.T) {
	s, a, err := createSupervisor(true, supervisor.StdIO)
	assert.NoError(t, err)

	a.On("Start", mock.Anything, mock.Anything).Return(nil)
	a.On("Send", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	_, _ = s.Send(context.Background(), nil)

	_, _ = s.Send(context.Background(), nil)

	a.AssertNumberOfCalls(t, "Start", 1)
}

func TestSend_Transient_DoesNotReuseWorker(t *testing.T) {
	s, a, err := createSupervisor(false, supervisor.StdIO)
	assert.NoError(t, err)

	a.On("Start", mock.Anything, mock.Anything).Return(nil)
	a.On("Stop", mock.Anything, mock.Anything).Return(nil, nil)
	a.On("Send", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	// boots first, transient worker
	_, _ = s.Send(context.Background(), nil)

	// boots second, transient worker
	_, _ = s.Send(context.Background(), nil)

	a.AssertNumberOfCalls(t, "Start", 2)
	a.AssertNumberOfCalls(t, "Stop", 2)
}

func TestSend_SendsData(t *testing.T) {
	s, a, err := createSupervisor(true, supervisor.StdIO)
	assert.NoError(t, err)

	a.On("Start", mock.Anything, mock.Anything).Return(nil)
	a.On("Send", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	_, err = s.Send(context.Background(), "data")
	assert.NoError(t, err)

	a.AssertCalled(t, "Send", mock.Anything, "data", mock.Anything)
}

// MARK: - mocks

type mockAdapter struct {
	mock.Mock
}

func (m *mockAdapter) Start(ctx context.Context, p worker.StartParams) error {
	args := m.Called(ctx, p)
	return args.Error(0)
}

func (m *mockAdapter) Stop(ctx context.Context, p worker.StopParams) (supervisor.WaitFunc, error) {
	args := m.Called(ctx, p)
	var f supervisor.WaitFunc
	if args.Get(0) != nil {
		f = args.Get(0).(supervisor.WaitFunc)
	}
	return f, args.Error(1)
}

func (m *mockAdapter) Send(ctx context.Context, d any, p worker.SendParams) (any, error) {
	args := m.Called(ctx, d, p)
	return args.Get(0), args.Error(1)
}

func createSupervisor(persistent bool, mode supervisor.IOMode) (
	*supervisor.Supervisor[any, any],
	*mockAdapter,
	error,
) {
	adapter := &mockAdapter{}

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
) (*supervisor.Supervisor[any, any], error) {
	s, err := supervisor.New(supervisor.SupervisorConfig[any, any]{
		Persistent:    persistent,
		Mode:          mode,
		WorkerFactory: factory,
		Log:           zap.NewNop(),
	})
	if err != nil {
		return nil, err
	}

	return s, nil
}

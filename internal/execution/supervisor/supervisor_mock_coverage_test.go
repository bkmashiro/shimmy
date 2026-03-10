package supervisor_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

// TestMockSupervisor_Send exercises the MockSupervisor.Send method.
func TestMockSupervisor_Send(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)

	data := map[string]any{"key": "value"}
	result := &supervisor.Result{Data: data}

	m.EXPECT().Send(context.Background(), "eval", data).Return(result, nil)

	res, err := m.Send(context.Background(), "eval", data)
	assert.NoError(t, err)
	assert.Equal(t, result, res)
}

// TestMockSupervisor_Send_Error exercises error return path.
func TestMockSupervisor_Send_Error(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)

	data := map[string]any{"key": "value"}
	m.EXPECT().Send(context.Background(), "eval", data).Return(nil, assert.AnError)

	_, err := m.Send(context.Background(), "eval", data)
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockSupervisor_Start exercises the Start method.
func TestMockSupervisor_Start(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)
	m.EXPECT().Start(context.Background()).Return(nil)

	err := m.Start(context.Background())
	assert.NoError(t, err)
}

// TestMockSupervisor_Start_Error exercises Start error path.
func TestMockSupervisor_Start_Error(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)
	m.EXPECT().Start(context.Background()).Return(assert.AnError)

	err := m.Start(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockSupervisor_Shutdown exercises the Shutdown method.
func TestMockSupervisor_Shutdown(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)

	wait := func() error { return nil }
	m.EXPECT().Shutdown(context.Background()).Return(wait, nil)

	fn, err := m.Shutdown(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, fn)
	assert.NoError(t, fn())
}

// TestMockSupervisor_Shutdown_Error exercises Shutdown error path.
func TestMockSupervisor_Shutdown_Error(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)
	m.EXPECT().Shutdown(context.Background()).Return(nil, assert.AnError)

	_, err := m.Shutdown(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockSupervisor_Suspend exercises the Suspend method.
func TestMockSupervisor_Suspend(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)

	wait := func() error { return nil }
	m.EXPECT().Suspend(context.Background()).Return(wait, nil)

	fn, err := m.Suspend(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, fn)
	assert.NoError(t, fn())
}

// TestMockSupervisor_Suspend_Error exercises Suspend error path.
func TestMockSupervisor_Suspend_Error(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)
	m.EXPECT().Suspend(context.Background()).Return(nil, assert.AnError)

	_, err := m.Suspend(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockSupervisor_Send_RunAndReturn exercises RunAndReturn path.
func TestMockSupervisor_Send_RunAndReturn(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)

	data := map[string]any{"key": "value"}
	m.EXPECT().Send(context.Background(), "eval", data).
		RunAndReturn(func(ctx context.Context, method string, d map[string]any) (*supervisor.Result, error) {
			return &supervisor.Result{Data: d}, nil
		})

	res, err := m.Send(context.Background(), "eval", data)
	assert.NoError(t, err)
	assert.Equal(t, data, res.Data)
}

// TestMockSupervisor_Start_RunAndReturn exercises RunAndReturn path.
func TestMockSupervisor_Start_RunAndReturn(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)
	m.EXPECT().Start(context.Background()).RunAndReturn(func(ctx context.Context) error {
		return nil
	})

	err := m.Start(context.Background())
	assert.NoError(t, err)
}

// TestMockSupervisor_Shutdown_RunAndReturn exercises RunAndReturn path.
func TestMockSupervisor_Shutdown_RunAndReturn(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)
	m.EXPECT().Shutdown(context.Background()).RunAndReturn(func(ctx context.Context) (supervisor.WaitFunc, error) {
		return func() error { return nil }, nil
	})

	fn, err := m.Shutdown(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, fn)
}

// TestMockSupervisor_Suspend_RunAndReturn exercises RunAndReturn path.
func TestMockSupervisor_Suspend_RunAndReturn(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)
	m.EXPECT().Suspend(context.Background()).RunAndReturn(func(ctx context.Context) (supervisor.WaitFunc, error) {
		return func() error { return nil }, nil
	})

	fn, err := m.Suspend(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, fn)
}

// TestMockSupervisor_Send_Run exercises the Run callback.
func TestMockSupervisor_Send_Run(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)

	var calledMethod string
	data := map[string]any{"x": 1}

	m.EXPECT().Send(context.Background(), "eval", data).
		Run(func(ctx context.Context, method string, d map[string]any) {
			calledMethod = method
		}).
		Return(&supervisor.Result{Data: data}, nil)

	_, _ = m.Send(context.Background(), "eval", data)
	assert.Equal(t, "eval", calledMethod)
}

// TestMockSupervisor_Start_Run exercises the Run callback for Start.
func TestMockSupervisor_Start_Run(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)

	var called bool
	m.EXPECT().Start(context.Background()).
		Run(func(ctx context.Context) {
			called = true
		}).
		Return(nil)

	_ = m.Start(context.Background())
	assert.True(t, called)
}

// TestMockSupervisor_Shutdown_Run exercises the Run callback for Shutdown.
func TestMockSupervisor_Shutdown_Run(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)

	var called bool
	m.EXPECT().Shutdown(context.Background()).
		Run(func(ctx context.Context) {
			called = true
		}).
		Return(func() error { return nil }, nil)

	_, _ = m.Shutdown(context.Background())
	assert.True(t, called)
}

// TestMockSupervisor_Suspend_Run exercises the Run callback for Suspend.
func TestMockSupervisor_Suspend_Run(t *testing.T) {
	m := supervisor.NewMockSupervisor(t)

	var called bool
	m.EXPECT().Suspend(context.Background()).
		Run(func(ctx context.Context) {
			called = true
		}).
		Return(func() error { return nil }, nil)

	_, _ = m.Suspend(context.Background())
	assert.True(t, called)
}

// TestSupervisor_Suspend_WithWait tests that Suspend returns a working wait func.
func TestSupervisor_Suspend_WithWait(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.FileIO)
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop().Return(nil, nil)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(nil, nil)

	_, _ = s.Send(context.Background(), "test", data)

	wait, err := s.Suspend(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, wait)
}

// TestSupervisor_Shutdown_WithWait tests that Shutdown returns a working wait func.
func TestSupervisor_Shutdown_WithWait(t *testing.T) {
	s, a, err := createSupervisor(t, supervisor.FileIO)
	assert.NoError(t, err)

	data := map[string]any{"data": "data"}

	a.EXPECT().Start(mock.Anything, mock.Anything).Return(nil)
	a.EXPECT().Stop().Return(func(ctx context.Context) error { return nil }, nil)
	a.EXPECT().Send(mock.Anything, "test", data, mock.Anything).Return(nil, nil)

	_, _ = s.Send(context.Background(), "test", data)

	wait, err := s.Shutdown(context.Background())
	assert.NoError(t, err)
	if wait != nil {
		err = wait()
		assert.NoError(t, err)
	}
}

// TestSupervisor_DefaultWorkerFactory exercises defaultWorkerFactory.
func TestSupervisor_DefaultWorkerFactory(t *testing.T) {
	s, err := supervisor.New(supervisor.Params{
		Config: supervisor.Config{
			IO: supervisor.IOConfig{Interface: supervisor.FileIO},
		},
		Context:       context.Background(),
		WorkerFactory: nil, // triggers defaultWorkerFactory
		Log:           zap.NewNop(),
	})
	assert.NoError(t, err)
	assert.NotNil(t, s)
}

// TestSupervisor_UnsupportedIOInterface tests error on unknown IO interface.
func TestSupervisor_UnsupportedIOInterface(t *testing.T) {
	s, err := supervisor.New(supervisor.Params{
		Config: supervisor.Config{
			IO: supervisor.IOConfig{Interface: supervisor.IOInterface("invalid")},
		},
		Context: context.Background(),
		Log:     zap.NewNop(),
	})
	assert.NoError(t, err)
	// Send will trigger worker creation which will fail due to unsupported interface
	_, err = s.Send(context.Background(), "test", map[string]any{})
	assert.ErrorIs(t, err, supervisor.ErrUnsupportedIOInterface)
}

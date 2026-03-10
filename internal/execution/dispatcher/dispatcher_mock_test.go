package dispatcher_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/dispatcher"
	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

// TestMockDispatcher_Send exercises the MockDispatcher.Send method.
func TestMockDispatcher_Send(t *testing.T) {
	m := dispatcher.NewMockDispatcher(t)

	data := map[string]any{"key": "value"}
	m.EXPECT().Send(context.Background(), "eval", data).Return(data, nil)

	res, err := m.Send(context.Background(), "eval", data)
	assert.NoError(t, err)
	assert.Equal(t, data, res)
}

// TestMockDispatcher_Send_Error exercises mock error return.
func TestMockDispatcher_Send_Error(t *testing.T) {
	m := dispatcher.NewMockDispatcher(t)

	data := map[string]any{"key": "value"}
	m.EXPECT().Send(context.Background(), "eval", data).Return(nil, assert.AnError)

	_, err := m.Send(context.Background(), "eval", data)
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockDispatcher_Start exercises the MockDispatcher.Start method.
func TestMockDispatcher_Start(t *testing.T) {
	m := dispatcher.NewMockDispatcher(t)
	m.EXPECT().Start(context.Background()).Return(nil)

	err := m.Start(context.Background())
	assert.NoError(t, err)
}

// TestMockDispatcher_Start_Error exercises mock Start error return.
func TestMockDispatcher_Start_Error(t *testing.T) {
	m := dispatcher.NewMockDispatcher(t)
	m.EXPECT().Start(context.Background()).Return(assert.AnError)

	err := m.Start(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockDispatcher_Shutdown exercises the MockDispatcher.Shutdown method.
func TestMockDispatcher_Shutdown(t *testing.T) {
	m := dispatcher.NewMockDispatcher(t)
	m.EXPECT().Shutdown(context.Background()).Return(nil)

	err := m.Shutdown(context.Background())
	assert.NoError(t, err)
}

// TestMockDispatcher_Shutdown_Error exercises mock Shutdown error return.
func TestMockDispatcher_Shutdown_Error(t *testing.T) {
	m := dispatcher.NewMockDispatcher(t)
	m.EXPECT().Shutdown(context.Background()).Return(assert.AnError)

	err := m.Shutdown(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockDispatcher_Send_RunAndReturn exercises RunAndReturn path.
func TestMockDispatcher_Send_RunAndReturn(t *testing.T) {
	m := dispatcher.NewMockDispatcher(t)

	data := map[string]any{"hello": "world"}
	m.EXPECT().Send(context.Background(), "test", data).
		RunAndReturn(func(ctx context.Context, method string, d map[string]any) (map[string]any, error) {
			return d, nil
		})

	res, err := m.Send(context.Background(), "test", data)
	assert.NoError(t, err)
	assert.Equal(t, data, res)
}

// TestMockDispatcher_Start_RunAndReturn exercises RunAndReturn for Start.
func TestMockDispatcher_Start_RunAndReturn(t *testing.T) {
	m := dispatcher.NewMockDispatcher(t)
	m.EXPECT().Start(context.Background()).RunAndReturn(func(ctx context.Context) error {
		return nil
	})

	err := m.Start(context.Background())
	assert.NoError(t, err)
}

// TestMockDispatcher_Shutdown_RunAndReturn exercises RunAndReturn for Shutdown.
func TestMockDispatcher_Shutdown_RunAndReturn(t *testing.T) {
	m := dispatcher.NewMockDispatcher(t)
	m.EXPECT().Shutdown(context.Background()).RunAndReturn(func(ctx context.Context) error {
		return nil
	})

	err := m.Shutdown(context.Background())
	assert.NoError(t, err)
}

// TestMockDispatcher_Send_Run exercises the Run callback path.
func TestMockDispatcher_Send_Run(t *testing.T) {
	m := dispatcher.NewMockDispatcher(t)

	var calledMethod string
	data := map[string]any{"x": 1}

	m.EXPECT().Send(context.Background(), "mymethod", data).
		Run(func(ctx context.Context, method string, d map[string]any) {
			calledMethod = method
		}).
		Return(data, nil)

	_, _ = m.Send(context.Background(), "mymethod", data)
	assert.Equal(t, "mymethod", calledMethod)
}

// TestMockDispatcher_Start_Run exercises the Run callback path for Start.
func TestMockDispatcher_Start_Run(t *testing.T) {
	m := dispatcher.NewMockDispatcher(t)

	var started bool
	m.EXPECT().Start(context.Background()).
		Run(func(ctx context.Context) {
			started = true
		}).
		Return(nil)

	_ = m.Start(context.Background())
	assert.True(t, started)
}

// TestMockDispatcher_Shutdown_Run exercises the Run callback path for Shutdown.
func TestMockDispatcher_Shutdown_Run(t *testing.T) {
	m := dispatcher.NewMockDispatcher(t)

	var shutdown bool
	m.EXPECT().Shutdown(context.Background()).
		Run(func(ctx context.Context) {
			shutdown = true
		}).
		Return(nil)

	_ = m.Shutdown(context.Background())
	assert.True(t, shutdown)
}

// TestDefaultSupervisorFactory_CalledViaPooled exercises defaultSupervisorFactory indirectly.
func TestDefaultSupervisorFactory_CalledViaPooled(t *testing.T) {
	// When SupervisorFactory is nil, the pooled dispatcher uses defaultSupervisorFactory.
	// We expect pool construction to succeed (it won't create supervisors until Acquire is called).
	d, err := dispatcher.NewPooledDispatcher(dispatcher.PooledDispatcherParams{
		Config: dispatcher.PooledDispatcherConfig{
			MaxWorkers: 1,
			Supervisor: supervisor.Config{},
		},
		Context:           context.Background(),
		SupervisorFactory: nil, // triggers defaultSupervisorFactory
		Log:               zap.NewNop(),
	})
	// Pool creation succeeds; actual supervisor creation happens on first Acquire.
	assert.NoError(t, err)
	assert.NotNil(t, d)
}

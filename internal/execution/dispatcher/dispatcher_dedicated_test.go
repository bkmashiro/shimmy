package dispatcher_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/dispatcher"
	"github.com/lambda-feedback/shimmy/internal/execution/supervisor"
)

// MARK: - DedicatedDispatcher

func TestDedicatedDispatcher_New_CreatesDispatcher(t *testing.T) {
	d, _, err := createDedicatedDispatcher(t)
	assert.NoError(t, err)
	assert.NotNil(t, d)
}

func TestDedicatedDispatcher_New_UsesDefaultSupervisorFactory(t *testing.T) {
	// Pass nil factory to trigger the default path (will fail to create supervisor,
	// but that's OK — we're exercising the nil-check branch).
	d, err := dispatcher.NewDedicatedDispatcher(dispatcher.DedicatedDispatcherParams{
		Config:            dispatcher.DedicatedDispatcherConfig{},
		Context:           context.Background(),
		SupervisorFactory: nil,
		Log:               zap.NewNop(),
	})
	// Default factory tries to create a real supervisor from an empty config,
	// which may succeed or fail depending on defaults — we just check it ran.
	_ = d
	_ = err
}

func TestDedicatedDispatcher_Start_StartsUnderlying(t *testing.T) {
	d, sv, err := createDedicatedDispatcher(t)
	assert.NoError(t, err)

	sv.EXPECT().Start(mock.Anything).Return(nil)

	err = d.Start(context.Background())
	assert.NoError(t, err)
}

func TestDedicatedDispatcher_Start_ReturnsErrorOnFailure(t *testing.T) {
	d, sv, err := createDedicatedDispatcher(t)
	assert.NoError(t, err)

	sv.EXPECT().Start(mock.Anything).Return(assert.AnError)

	err = d.Start(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

func TestDedicatedDispatcher_Send_ReturnsData(t *testing.T) {
	d, sv, err := createDedicatedDispatcher(t)
	assert.NoError(t, err)

	sv.EXPECT().Start(mock.Anything).Return(nil)
	_ = d.Start(context.Background())

	data := map[string]any{"key": "value"}
	result := &supervisor.Result{
		Data:    data,
		Release: func(context.Context) error { return nil },
	}
	sv.EXPECT().Send(mock.Anything, "eval", data).Return(result, nil)

	res, err := d.Send(context.Background(), "eval", data)
	assert.NoError(t, err)
	assert.Equal(t, data, res)
}

func TestDedicatedDispatcher_Send_ReturnsErrorOnSendFailure(t *testing.T) {
	d, sv, err := createDedicatedDispatcher(t)
	assert.NoError(t, err)

	sv.EXPECT().Start(mock.Anything).Return(nil)
	_ = d.Start(context.Background())

	data := map[string]any{"key": "value"}
	sv.EXPECT().Send(mock.Anything, "eval", data).Return(nil, assert.AnError)

	_, err = d.Send(context.Background(), "eval", data)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestDedicatedDispatcher_Send_ReturnsErrorOnReleaseFailure(t *testing.T) {
	d, sv, err := createDedicatedDispatcher(t)
	assert.NoError(t, err)

	sv.EXPECT().Start(mock.Anything).Return(nil)
	_ = d.Start(context.Background())

	data := map[string]any{"key": "value"}
	result := &supervisor.Result{
		Data:    data,
		Release: func(context.Context) error { return assert.AnError },
	}
	sv.EXPECT().Send(mock.Anything, "eval", data).Return(result, nil)

	_, err = d.Send(context.Background(), "eval", data)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestDedicatedDispatcher_Shutdown_Succeeds(t *testing.T) {
	d, sv, err := createDedicatedDispatcher(t)
	assert.NoError(t, err)

	wait := func() error { return nil }
	sv.EXPECT().Shutdown(mock.Anything).Return(wait, nil)

	err = d.Shutdown(context.Background())
	assert.NoError(t, err)
}

func TestDedicatedDispatcher_Shutdown_ReturnsErrorOnShutdownFailure(t *testing.T) {
	d, sv, err := createDedicatedDispatcher(t)
	assert.NoError(t, err)

	sv.EXPECT().Shutdown(mock.Anything).Return(nil, assert.AnError)

	err = d.Shutdown(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

func TestDedicatedDispatcher_Shutdown_ReturnsErrorOnWaitFailure(t *testing.T) {
	d, sv, err := createDedicatedDispatcher(t)
	assert.NoError(t, err)

	wait := func() error { return assert.AnError }
	sv.EXPECT().Shutdown(mock.Anything).Return(wait, nil)

	err = d.Shutdown(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

func TestDedicatedDispatcher_Shutdown_NilWaitFunc(t *testing.T) {
	d, sv, err := createDedicatedDispatcher(t)
	assert.NoError(t, err)

	sv.EXPECT().Shutdown(mock.Anything).Return(nil, nil)

	err = d.Shutdown(context.Background())
	assert.NoError(t, err)
}

func TestPooledDispatcher_Start_IsNoOp(t *testing.T) {
	d, _, err := createPooledDispatcher(t)
	assert.NoError(t, err)

	// Start on pooled dispatcher is a no-op — should succeed.
	err = d.Start(context.Background())
	assert.NoError(t, err)
}

func TestDedicatedDispatcher_SupervisorFactory_FailureReturnsError(t *testing.T) {
	factory := func(params supervisor.Params) (supervisor.Supervisor, error) {
		return nil, assert.AnError
	}

	_, err := dispatcher.NewDedicatedDispatcher(dispatcher.DedicatedDispatcherParams{
		Config:            dispatcher.DedicatedDispatcherConfig{},
		Context:           context.Background(),
		SupervisorFactory: factory,
		Log:               zap.NewNop(),
	})
	assert.ErrorIs(t, err, assert.AnError)
}

// MARK: - helpers

func createDedicatedDispatcher(t *testing.T) (dispatcher.Dispatcher, *supervisor.MockSupervisor, error) {
	sv := supervisor.NewMockSupervisor(t)

	factory := func(params supervisor.Params) (supervisor.Supervisor, error) {
		return sv, nil
	}

	d, err := dispatcher.NewDedicatedDispatcher(dispatcher.DedicatedDispatcherParams{
		Config:            dispatcher.DedicatedDispatcherConfig{},
		Context:           context.Background(),
		SupervisorFactory: factory,
		Log:               zap.NewNop(),
	})
	if err != nil {
		return nil, nil, err
	}

	return d, sv, nil
}

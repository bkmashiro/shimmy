package supervisor

import (
	"context"
	"testing"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

func createStdioAdapter(t *testing.T) (*stdioAdapter[any, any], *worker.MockWorker) {
	w := worker.NewMockWorker(t)

	workerFactory := func(context.Context, worker.StartConfig) (worker.Worker, error) {
		return w, nil
	}

	adapter := &stdioAdapter[any, any]{
		workerFactory: workerFactory,
		log:           zap.NewNop(),
	}

	return adapter, w
}

func TestStdioAdapter_Start(t *testing.T) {
	a, w := createStdioAdapter(t)

	ctx := context.Background()
	params := worker.StartConfig{}

	w.EXPECT().Start(ctx).Return(nil)

	err := a.Start(ctx, params)
	assert.NoError(t, err)
}

func TestStdioAdapter_Start_PassesError(t *testing.T) {
	a, w := createStdioAdapter(t)

	ctx := context.Background()
	params := worker.StartConfig{}

	w.EXPECT().Start(ctx).Return(assert.AnError)

	err := a.Start(ctx, params)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestStdioAdapter_Stop(t *testing.T) {
	a, w := createStdioAdapter(t)

	w.EXPECT().Start(mock.Anything).Return(nil)
	w.EXPECT().Terminate().Return(nil)

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.NoError(t, err)

	_, err = a.Stop(worker.StopConfig{})
	assert.NoError(t, err)
}

func TestStdioAdapter_Stop_FailsIfNotStarted(t *testing.T) {
	a, _ := createStdioAdapter(t)

	_, err := a.Stop(worker.StopConfig{})
	assert.Error(t, err)
}

func TestStdioAdapter_Stop_PassesError(t *testing.T) {
	a, w := createStdioAdapter(t)

	w.EXPECT().Start(mock.Anything).Return(nil)
	w.EXPECT().Terminate().Return(assert.AnError)

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.NoError(t, err)

	_, err = a.Stop(worker.StopConfig{})
	assert.ErrorIs(t, err, assert.AnError)
}

func TestStdioAdapter_Stop_WaitFor(t *testing.T) {
	a, w := createStdioAdapter(t)

	ctx := context.Background()
	params := worker.StopConfig{Timeout: 10}

	w.EXPECT().Start(mock.Anything).Return(nil)
	w.EXPECT().Terminate().Return(nil)
	w.EXPECT().WaitFor(ctx, params.Timeout).Return(worker.ExitEvent{}, nil)

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.NoError(t, err)

	wait, err := a.Stop(params)
	assert.NoError(t, err)

	err = wait(ctx)
	assert.NoError(t, err)
}

func TestStdioAdapter_Stop_WaitForError(t *testing.T) {
	a, w := createStdioAdapter(t)

	ctx := context.Background()
	params := worker.StopConfig{Timeout: 10}

	w.EXPECT().Start(mock.Anything).Return(nil)
	w.EXPECT().Terminate().Return(nil)
	w.EXPECT().WaitFor(ctx, params.Timeout).Return(worker.ExitEvent{}, assert.AnError)

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.NoError(t, err)

	wait, err := a.Stop(params)
	assert.NoError(t, err)

	err = wait(ctx)
	assert.ErrorIs(t, err, assert.AnError)
}

// func TestStdioAdapter_Send(t *testing.T) {
// 	a, w := createStdioAdapter(t)

// 	ctx := context.Background()
// 	data := "test"

// 	w.EXPECT().Send(ctx, data, 0).Return("result", nil)

// 	res, err := a.Send(ctx, data, 0)
// 	assert.NoError(t, err)
// 	assert.Equal(t, "result", res)
// }

// func TestStdioAdapter_Send_PassesError(t *testing.T) {
// 	a, w := createStdioAdapter(t)

// 	ctx := context.Background()
// 	data := "test"

// 	w.EXPECT().Send(ctx, data, 0).Return("result", assert.AnError)

// 	_, err := a.Send(ctx, data, 0)
// 	assert.ErrorIs(t, err, assert.AnError)
// }

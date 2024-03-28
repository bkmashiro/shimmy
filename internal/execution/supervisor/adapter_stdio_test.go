package supervisor

import (
	"context"
	"testing"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func createStdioAdapter(t *testing.T) (*stdioAdapter[any, any], *worker.MockWorker[any, any]) {
	worker := worker.NewMockWorker[any, any](t)

	adapter := &stdioAdapter[any, any]{
		worker: worker,
		log:    zap.NewNop(),
	}

	return adapter, worker
}

func TestStdioAdapter_Start(t *testing.T) {
	a, w := createStdioAdapter(t)

	ctx := context.Background()
	params := worker.StartConfig{}

	w.EXPECT().Start(ctx, params).Return(nil)

	err := a.Start(ctx, params)
	assert.NoError(t, err)
}

func TestStdioAdapter_Start_PassesError(t *testing.T) {
	a, w := createStdioAdapter(t)

	ctx := context.Background()
	params := worker.StartConfig{}

	w.EXPECT().Start(ctx, params).Return(assert.AnError)

	err := a.Start(ctx, params)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestStdioAdapter_Stop(t *testing.T) {
	a, w := createStdioAdapter(t)

	w.EXPECT().Terminate().Return(nil)

	_, err := a.Stop(context.Background(), worker.StopConfig{})
	assert.NoError(t, err)
}

func TestStdioAdapter_Stop_PassesError(t *testing.T) {
	a, w := createStdioAdapter(t)

	w.EXPECT().Terminate().Return(assert.AnError)

	_, err := a.Stop(context.Background(), worker.StopConfig{})
	assert.ErrorIs(t, err, assert.AnError)
}

func TestStdioAdapter_Stop_WaitFor(t *testing.T) {
	a, w := createStdioAdapter(t)

	ctx := context.Background()
	params := worker.StopConfig{Timeout: 10}

	w.EXPECT().Terminate().Return(nil)
	w.EXPECT().WaitFor(ctx, params.Timeout).Return(worker.ExitEvent{}, nil)

	wait, err := a.Stop(ctx, params)
	assert.NoError(t, err)

	err = wait()
	assert.NoError(t, err)
}

func TestStdioAdapter_Stop_WaitForError(t *testing.T) {
	a, w := createStdioAdapter(t)

	ctx := context.Background()
	params := worker.StopConfig{Timeout: 10}

	w.EXPECT().Terminate().Return(nil)
	w.EXPECT().WaitFor(ctx, params.Timeout).Return(worker.ExitEvent{}, assert.AnError)

	wait, err := a.Stop(ctx, params)
	assert.NoError(t, err)

	err = wait()
	assert.ErrorIs(t, err, assert.AnError)
}

func TestStdioAdapter_Send(t *testing.T) {
	a, w := createStdioAdapter(t)

	ctx := context.Background()
	data := "test"
	params := worker.SendConfig{}

	w.EXPECT().Send(ctx, data, params).Return("result", nil)

	res, err := a.Send(ctx, data, params)
	assert.NoError(t, err)
	assert.Equal(t, "result", res)
}

func TestStdioAdapter_Send_PassesError(t *testing.T) {
	a, w := createStdioAdapter(t)

	ctx := context.Background()
	data := "test"
	params := worker.SendConfig{}

	w.EXPECT().Send(ctx, data, params).Return("result", assert.AnError)

	_, err := a.Send(ctx, data, params)
	assert.ErrorIs(t, err, assert.AnError)
}

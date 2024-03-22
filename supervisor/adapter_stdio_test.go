package supervisor

import (
	"context"
	"errors"
	"testing"

	"github.com/lambda-feedback/shimmy/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

func createStdioAdapter() (*stdioAdapter[any, any], *mockWorker) {
	worker := &mockWorker{}

	adapter := &stdioAdapter[any, any]{
		worker: worker,
		log:    zap.NewNop(),
	}

	return adapter, worker
}

func TestStdioAdapter_Start(t *testing.T) {
	a, w := createStdioAdapter()

	w.On("Start", mock.Anything, mock.Anything).Return(nil)

	ctx := context.Background()
	params := worker.StartParams{}

	err := a.Start(ctx, params)
	assert.NoError(t, err)

	w.AssertCalled(t, "Start", ctx, params)
}

func TestStdioAdapter_Start_PassesError(t *testing.T) {
	a, w := createStdioAdapter()

	w.On("Start", mock.Anything, mock.Anything).Return(errors.New("test error"))

	ctx := context.Background()
	params := worker.StartParams{}

	err := a.Start(ctx, params)
	assert.ErrorContains(t, err, "test error")

	w.AssertCalled(t, "Start", ctx, params)
}

func TestStdioAdapter_Stop(t *testing.T) {
	a, w := createStdioAdapter()

	w.On("Terminate").Return(nil)

	_, err := a.Stop(context.Background(), worker.StopParams{})
	assert.NoError(t, err)

	w.AssertCalled(t, "Terminate")
}

func TestStdioAdapter_Stop_PassesError(t *testing.T) {
	a, w := createStdioAdapter()

	w.On("Terminate").Return(errors.New("test error"))

	_, err := a.Stop(context.Background(), worker.StopParams{})
	assert.ErrorContains(t, err, "test error")

	w.AssertCalled(t, "Terminate")
}

func TestStdioAdapter_Stop_WaitFor(t *testing.T) {
	a, w := createStdioAdapter()

	w.On("Terminate").Return(nil)
	w.On("WaitFor", mock.Anything, mock.Anything).Return(worker.ExitEvent{}, nil)

	ctx := context.Background()
	params := worker.StopParams{Timeout: 10}

	wait, err := a.Stop(ctx, params)
	assert.NoError(t, err)

	err = wait()
	assert.NoError(t, err)

	w.AssertCalled(t, "Terminate")
	w.AssertCalled(t, "WaitFor", ctx, params.Timeout)
}

func TestStdioAdapter_Send(t *testing.T) {
	a, w := createStdioAdapter()

	w.On("Send", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	ctx := context.Background()
	data := "test"
	params := worker.SendParams{}

	_, err := a.Send(ctx, data, params)
	assert.NoError(t, err)

	w.AssertCalled(t, "Send", ctx, data, params)
}

func TestStdioAdapter_Send_PassesError(t *testing.T) {
	a, w := createStdioAdapter()

	w.On("Send", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("test error"))

	ctx := context.Background()
	data := "test"
	params := worker.SendParams{}

	_, err := a.Send(ctx, data, params)
	assert.ErrorContains(t, err, "test error")

	w.AssertCalled(t, "Send", ctx, data, params)
}

func TestStdioAdapter_Send_PassesResult(t *testing.T) {
	a, w := createStdioAdapter()

	w.On("Send", mock.Anything, mock.Anything, mock.Anything).Return("result", nil)

	ctx := context.Background()
	data := "test"
	params := worker.SendParams{}

	res, err := a.Send(ctx, data, params)
	assert.NoError(t, err)
	assert.Equal(t, "result", res)

	w.AssertCalled(t, "Send", ctx, data, params)
}

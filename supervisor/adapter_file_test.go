package supervisor

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/lambda-feedback/shimmy/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

func createFileAdapter() (*fileAdapter[any, any], *mockWorker) {
	worker := &mockWorker{}

	adapter := &fileAdapter[any, any]{
		worker: worker,
		log:    zap.NewNop(),
	}

	return adapter, worker
}

func TestFileAdapter_Start(t *testing.T) {
	a, w := createFileAdapter()

	w.On("Start", mock.Anything, mock.Anything).Return(nil)

	err := a.Start(context.Background(), worker.StartParams{})
	assert.NoError(t, err)

	w.AssertNotCalled(t, "Start")
}

func TestFileAdapter_Stop(t *testing.T) {
	a, w := createFileAdapter()

	w.On("Terminate").Return(nil)

	_, err := a.Stop(context.Background(), worker.StopParams{})
	assert.NoError(t, err)

	w.AssertCalled(t, "Terminate")
}

func TestFileAdapter_Stop_PassesError(t *testing.T) {
	a, w := createFileAdapter()

	w.On("Terminate").Return(errors.New("test error"))

	_, err := a.Stop(context.Background(), worker.StopParams{})
	assert.ErrorContains(t, err, "test error")

	w.AssertCalled(t, "Terminate")
}

func TestFileAdapter_Stop_WaitFor(t *testing.T) {
	a, w := createFileAdapter()

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

func TestFileAdapter_Send(t *testing.T) {
	a, w := createFileAdapter()

	// for the adapter to succeed, the worker process must write to
	// the response file before exiting. we mock this behaviour here.
	w.On("Start", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		params := args.Get(1).(worker.StartParams)
		data, _ := os.ReadFile(params.Env["REQUEST_FILE_NAME"])
		_ = os.WriteFile(params.Env["RESPONSE_FILE_NAME"], data, os.ModeAppend)
	}).Return(nil)
	w.On("WaitFor", mock.Anything, mock.Anything).Return(worker.ExitEvent{}, nil)

	ctx := context.Background()
	data := map[string]any{"foo": "bar"}
	params := worker.SendParams{Timeout: 10}

	res, err := a.Send(ctx, data, params)
	assert.NoError(t, err)
	assert.Equal(t, data, res)

	w.AssertCalled(t, "Start", mock.Anything, mock.Anything)
	w.AssertCalled(t, "WaitFor", ctx, params.Timeout)
}

func TestFileAdapter_Send_ReturnsStartError(t *testing.T) {
	a, w := createFileAdapter()

	w.On("Start", mock.Anything, mock.Anything).Return(errors.New("test error"))

	ctx := context.Background()

	_, err := a.Send(ctx, "test", worker.SendParams{})
	assert.ErrorContains(t, err, "test error")

	w.AssertCalled(t, "Start", ctx, mock.Anything)
}

func TestFileAdapter_Send_ReturnsWaitForError(t *testing.T) {
	a, w := createFileAdapter()

	w.On("Start", mock.Anything, mock.Anything).Return(nil)
	w.On("WaitFor", mock.Anything, mock.Anything).Return(worker.ExitEvent{}, errors.New("test error"))

	ctx := context.Background()

	_, err := a.Send(ctx, "test", worker.SendParams{})
	assert.ErrorContains(t, err, "test error")

	w.AssertCalled(t, "Start", ctx, mock.Anything)
	w.AssertCalled(t, "WaitFor", ctx, mock.Anything)
}

func TestFileAdapter_Send_ReturnsReadError(t *testing.T) {
	a, w := createFileAdapter()

	w.On("Start", mock.Anything, mock.Anything).Return(nil)
	w.On("WaitFor", mock.Anything, mock.Anything).Return(worker.ExitEvent{}, nil)

	ctx := context.Background()

	_, err := a.Send(ctx, "test", worker.SendParams{})
	assert.ErrorIs(t, err, io.EOF)

	w.AssertCalled(t, "Start", ctx, mock.Anything)
	w.AssertCalled(t, "WaitFor", ctx, mock.Anything)
}

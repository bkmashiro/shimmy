package supervisor

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

func createFileAdapter(t *testing.T) (*fileAdapter[any, any], *worker.MockWorker[any, any]) {
	worker := worker.NewMockWorker[any, any](t)

	adapter := &fileAdapter[any, any]{
		worker: worker,
		log:    zap.NewNop(),
	}

	return adapter, worker
}

func TestFileAdapter_Start(t *testing.T) {
	a, w := createFileAdapter(t)

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.NoError(t, err)

	w.AssertNotCalled(t, "Start")
}

func TestFileAdapter_Stop(t *testing.T) {
	a, w := createFileAdapter(t)

	w.EXPECT().Terminate().Return(nil)

	_, err := a.Stop(context.Background(), worker.StopConfig{})
	assert.NoError(t, err)
}

func TestFileAdapter_Stop_PassesError(t *testing.T) {
	a, w := createFileAdapter(t)

	w.EXPECT().Terminate().Return(assert.AnError)

	_, err := a.Stop(context.Background(), worker.StopConfig{})
	assert.ErrorIs(t, err, assert.AnError)
}

func TestFileAdapter_Stop_WaitFor(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()
	params := worker.StopConfig{Timeout: 10}

	w.EXPECT().Terminate().Return(nil)
	w.EXPECT().WaitFor(mock.Anything, params.Timeout).Return(worker.ExitEvent{}, nil)

	wait, err := a.Stop(ctx, params)
	assert.NoError(t, err)

	err = wait()
	assert.NoError(t, err)
}

func TestFileAdapter_Send(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()
	data := map[string]any{"foo": "bar"}
	params := worker.SendConfig{Timeout: 10}

	// for the adapter to succeed, the worker process must write to
	// the response file before exiting. we mock this behaviour here.
	w.EXPECT().Start(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, sp worker.StartConfig) error {
		data, _ := os.ReadFile(sp.Env["REQUEST_FILE_NAME"])
		_ = os.WriteFile(sp.Env["RESPONSE_FILE_NAME"], data, os.ModeAppend)
		return nil
	})
	w.EXPECT().WaitFor(mock.Anything, params.Timeout).Return(worker.ExitEvent{}, nil)

	res, err := a.Send(ctx, data, params)
	assert.NoError(t, err)
	assert.Equal(t, data, res)
}

func TestFileAdapter_Send_ReturnsStartError(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()
	data := map[string]any{"foo": "bar"}

	w.EXPECT().Start(ctx, mock.Anything).Return(assert.AnError)

	_, err := a.Send(ctx, data, worker.SendConfig{})
	assert.ErrorIs(t, err, assert.AnError)
}

func TestFileAdapter_Send_ReturnsWaitForError(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()
	data := map[string]any{"foo": "bar"}

	w.EXPECT().Start(ctx, mock.Anything).Return(nil)
	w.EXPECT().WaitFor(ctx, mock.Anything).Return(worker.ExitEvent{}, assert.AnError)

	_, err := a.Send(ctx, data, worker.SendConfig{})
	assert.ErrorIs(t, err, assert.AnError)
}

func TestFileAdapter_Send_ReturnsReadError(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()
	data := map[string]any{"foo": "bar"}

	w.EXPECT().Start(ctx, mock.Anything).Return(nil)
	w.EXPECT().WaitFor(ctx, mock.Anything).Return(worker.ExitEvent{}, nil)

	_, err := a.Send(ctx, data, worker.SendConfig{})
	assert.ErrorIs(t, err, io.EOF)
}

func TestFileAdapter_Send_ReturnsInvalidDataError(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()

	// write invalid data to request file
	res, err := a.Send(ctx, make(chan int), worker.SendConfig{})
	assert.Error(t, err)
	assert.Nil(t, res)

	w.AssertNotCalled(t, "Start")
}

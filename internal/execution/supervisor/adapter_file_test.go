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

func createFileAdapter(t *testing.T) (*fileAdapter[any, any], *worker.MockWorker) {
	worker := worker.NewMockWorker(t)

	adapter := &fileAdapter[any, any]{
		worker: worker,
		log:    zap.NewNop(),
	}

	return adapter, worker
}

func TestFileAdapter_Start_DoesNotStartWorker(t *testing.T) {
	a, w := createFileAdapter(t)

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.NoError(t, err)

	w.AssertNotCalled(t, "Start")
}

func TestFileAdapter_Stop_DoesNotStopWorker(t *testing.T) {
	a, w := createFileAdapter(t)

	_, err := a.Stop(worker.StopConfig{})
	assert.NoError(t, err)

	w.AssertNotCalled(t, "Terminate")
}

func TestFileAdapter_Send(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()
	data := map[string]any{"foo": "bar"}

	// for the adapter to succeed, the worker process must write to
	// the response file before exiting. we mock this behaviour here.
	w.EXPECT().Start(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, sp worker.StartConfig) error {
		data, _ := os.ReadFile(sp.Args[len(sp.Args)-2])
		_ = os.WriteFile(sp.Args[len(sp.Args)-1], data, os.ModeAppend)
		return nil
	})
	var cell int
	w.EXPECT().WaitFor(mock.Anything, mock.Anything).Return(worker.ExitEvent{Code: &cell}, nil)

	res, err := a.Send(ctx, data, 10)
	assert.NoError(t, err)
	assert.Equal(t, data, res)
}

func TestFileAdapter_Send_ReturnsStartError(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()
	data := map[string]any{"foo": "bar"}

	w.EXPECT().Start(ctx, mock.Anything).Return(assert.AnError)

	_, err := a.Send(ctx, data, 0)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestFileAdapter_Send_ReturnsWaitForError(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()
	data := map[string]any{"foo": "bar"}

	w.EXPECT().Start(ctx, mock.Anything).Return(nil)
	w.EXPECT().WaitFor(ctx, mock.Anything).Return(worker.ExitEvent{}, assert.AnError)

	_, err := a.Send(ctx, data, 0)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestFileAdapter_Send_ReturnsReadError(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()
	data := map[string]any{"foo": "bar"}

	w.EXPECT().Start(ctx, mock.Anything).Return(nil)
	var cell int
	w.EXPECT().WaitFor(ctx, mock.Anything).Return(worker.ExitEvent{Code: &cell}, nil)

	_, err := a.Send(ctx, data, 0)
	assert.ErrorIs(t, err, io.EOF)
}

func TestFileAdapter_Send_ReturnsInvalidDataError(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()

	// write invalid data to request file
	res, err := a.Send(ctx, make(chan int), 0)
	assert.Error(t, err)
	assert.Nil(t, res)

	w.AssertNotCalled(t, "Start")
}

package supervisor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
)

func TestFileAdapter_Start_DoesNotStartWorker(t *testing.T) {
	a, w := createFileAdapter(t)

	err := a.Start(context.Background(), worker.StartConfig{})
	assert.NoError(t, err)

	w.AssertNotCalled(t, "Start")
}

func TestBoundedLogBuffer_KeepsTailAndMarksTruncation(t *testing.T) {
	buffer := boundedLogBuffer{limit: 8}

	n, err := buffer.Write([]byte("12345"))
	assert.NoError(t, err)
	assert.Equal(t, 5, n)
	n, err = buffer.Write([]byte("67890"))
	assert.NoError(t, err)
	assert.Equal(t, 5, n)

	assert.Equal(t, 8, buffer.Len())
	assert.Equal(t, fileAdapterStdoutTruncatedMarker+"34567890", buffer.String())
}

func TestFileAdapter_Stop_DoesNotStopWorker(t *testing.T) {
	a, w := createFileAdapter(t)

	_, err := a.Stop()
	assert.NoError(t, err)

	w.AssertNotCalled(t, "Terminate")
}

func TestFileAdapter_Send(t *testing.T) {
	w := worker.NewMockWorker(t)

	var sp *worker.StartConfig

	workerFactory := func(params worker.StartConfig) (worker.Worker, error) {
		sp = &params
		return w, nil
	}

	a := &fileAdapter{
		workerFactory: workerFactory,
		log:           zap.NewNop(),
	}

	ctx := context.Background()
	data := map[string]any{"foo": "bar"}

	var requestFileName string
	var responseFileName string

	// for the adapter to succeed, the worker process must write to
	// the response file before exiting. we mock this behaviour here.
	w.EXPECT().Start(mock.Anything).RunAndReturn(func(ctx context.Context) error {
		requestFileName = sp.Args[len(sp.Args)-2]
		responseFileName = sp.Args[len(sp.Args)-1]
		data, _ := os.ReadFile(requestFileName)
		_ = os.WriteFile(responseFileName, data, os.ModeAppend)
		return nil
	})
	w.EXPECT().ReadPipe().Return(io.NopCloser(strings.NewReader("")), nil)
	var cell int
	w.EXPECT().Wait(mock.Anything).Return(worker.ExitEvent{Code: &cell}, nil)

	res, err := a.Send(ctx, "test", data, 10)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"command": "test", "params": data}, res)

	// check that the request and response files were cleaned up
	_, err = os.Stat(requestFileName)
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(responseFileName)
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Dir(requestFileName))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestFileAdapter_Send_ReturnsStartError(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()
	data := map[string]any{"foo": "bar"}

	w.EXPECT().ReadPipe().Return(io.NopCloser(strings.NewReader("")), nil)
	w.EXPECT().Start(mock.Anything).Return(assert.AnError)

	_, err := a.Send(ctx, "test", data, 0)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestFileAdapter_Send_ReturnsWaitForError(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()
	data := map[string]any{"foo": "bar"}

	w.EXPECT().Start(mock.Anything).Return(nil)
	w.EXPECT().ReadPipe().Return(io.NopCloser(strings.NewReader("")), nil)
	w.EXPECT().Wait(mock.Anything).Return(worker.ExitEvent{}, assert.AnError)

	_, err := a.Send(ctx, "test", data, 0)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestFileAdapter_Send_ReturnsReadError(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()
	data := map[string]any{"foo": "bar"}

	w.EXPECT().Start(mock.Anything).Return(nil)
	w.EXPECT().ReadPipe().Return(io.NopCloser(strings.NewReader("")), nil)
	var cell int
	w.EXPECT().Wait(mock.Anything).Return(worker.ExitEvent{Code: &cell}, nil)

	_, err := a.Send(ctx, "test", data, 0)
	assert.ErrorIs(t, err, io.EOF)
}

func TestFileAdapter_Send_ReturnsInvalidDataError(t *testing.T) {
	a, w := createFileAdapter(t)

	ctx := context.Background()
	data := map[string]any{"foo": make(chan int)}
	entriesBefore := fileAdapterWorkingDirectoryEntries(t)

	res, err := a.Send(ctx, "test", data, 0)
	assert.Error(t, err)
	assert.Nil(t, res)

	w.AssertNotCalled(t, "Start")
	assert.Equal(t, entriesBefore, fileAdapterWorkingDirectoryEntries(t))
}

func createFileAdapter(t *testing.T) (*fileAdapter, *worker.MockWorker) {
	w := worker.NewMockWorker(t)

	workerFactory := func(params worker.StartConfig) (worker.Worker, error) {
		return w, nil
	}

	adapter := &fileAdapter{
		workerFactory: workerFactory,
		log:           zap.NewNop(),
	}

	return adapter, w
}

func fileAdapterWorkingDirectoryEntries(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(os.TempDir(), "shimmy"))
	if os.IsNotExist(err) {
		return nil
	}
	assert.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

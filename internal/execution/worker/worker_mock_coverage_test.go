package worker_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
)

// TestMockWorker_DuplexPipe exercises the mock DuplexPipe method.
func TestMockWorker_DuplexPipe(t *testing.T) {
	m := worker.NewMockWorker(t)

	// Use a real process worker's DuplexPipe as a ReadWriteCloser
	w := newProcessWorkerForTest(t)
	rwc, err := w.DuplexPipe()
	if err != nil {
		t.Skip("could not obtain duplex pipe")
	}

	m.EXPECT().DuplexPipe().Return(rwc, nil)

	result, err := m.DuplexPipe()
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

// TestMockWorker_DuplexPipe_Error tests error path.
func TestMockWorker_DuplexPipe_Error(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().DuplexPipe().Return(nil, assert.AnError)

	_, err := m.DuplexPipe()
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockWorker_ReadPipe exercises the mock ReadPipe method.
func TestMockWorker_ReadPipe(t *testing.T) {
	m := worker.NewMockWorker(t)

	w := newProcessWorkerForTest(t)
	rc, err := w.ReadPipe()
	if err != nil {
		t.Skip("could not obtain read pipe")
	}

	m.EXPECT().ReadPipe().Return(rc, nil)

	result, err := m.ReadPipe()
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

// TestMockWorker_ReadPipe_Error tests error path.
func TestMockWorker_ReadPipe_Error(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().ReadPipe().Return(nil, assert.AnError)

	_, err := m.ReadPipe()
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockWorker_WritePipe exercises the mock WritePipe method.
func TestMockWorker_WritePipe(t *testing.T) {
	m := worker.NewMockWorker(t)

	w := newProcessWorkerForTest(t)
	pipe, err := w.WritePipe()
	if err != nil {
		t.Skip("could not get a real write pipe")
	}

	m.EXPECT().WritePipe().Return(pipe, nil)

	result, err := m.WritePipe()
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

// TestMockWorker_WritePipe_Error tests error path.
func TestMockWorker_WritePipe_Error(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().WritePipe().Return(nil, assert.AnError)

	_, err := m.WritePipe()
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockWorker_Start exercises the mock Start method.
func TestMockWorker_Start(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().Start(context.Background()).Return(nil)

	err := m.Start(context.Background())
	assert.NoError(t, err)
}

// TestMockWorker_Start_Error exercises Start error path.
func TestMockWorker_Start_Error(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().Start(context.Background()).Return(assert.AnError)

	err := m.Start(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockWorker_Stop exercises the mock Stop method.
func TestMockWorker_Stop(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().Stop().Return(nil)

	err := m.Stop()
	assert.NoError(t, err)
}

// TestMockWorker_Stop_Error exercises Stop error path.
func TestMockWorker_Stop_Error(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().Stop().Return(assert.AnError)

	err := m.Stop()
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockWorker_Wait exercises the mock Wait method.
func TestMockWorker_Wait(t *testing.T) {
	m := worker.NewMockWorker(t)

	code := 0
	evt := worker.ExitEvent{Code: &code}
	m.EXPECT().Wait(context.Background()).Return(evt, nil)

	result, err := m.Wait(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, evt, result)
}

// TestMockWorker_Wait_Error exercises Wait error path.
func TestMockWorker_Wait_Error(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().Wait(context.Background()).Return(worker.ExitEvent{}, assert.AnError)

	_, err := m.Wait(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockWorker_WaitFor exercises the mock WaitFor method.
func TestMockWorker_WaitFor(t *testing.T) {
	m := worker.NewMockWorker(t)

	code := 0
	evt := worker.ExitEvent{Code: &code}
	m.EXPECT().WaitFor(context.Background(), 5*time.Second).Return(evt, nil)

	result, err := m.WaitFor(context.Background(), 5*time.Second)
	assert.NoError(t, err)
	assert.Equal(t, evt, result)
}

// TestMockWorker_WaitFor_Error exercises WaitFor error path.
func TestMockWorker_WaitFor_Error(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().WaitFor(context.Background(), time.Second).Return(worker.ExitEvent{}, assert.AnError)

	_, err := m.WaitFor(context.Background(), time.Second)
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockWorker_RunAndReturn exercises RunAndReturn paths.
func TestMockWorker_Start_RunAndReturn(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().Start(context.Background()).RunAndReturn(func(ctx context.Context) error {
		return nil
	})

	err := m.Start(context.Background())
	assert.NoError(t, err)
}

func TestMockWorker_Stop_RunAndReturn(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().Stop().RunAndReturn(func() error {
		return nil
	})

	err := m.Stop()
	assert.NoError(t, err)
}

func TestMockWorker_Wait_RunAndReturn(t *testing.T) {
	m := worker.NewMockWorker(t)
	code := 0
	m.EXPECT().Wait(context.Background()).RunAndReturn(func(ctx context.Context) (worker.ExitEvent, error) {
		return worker.ExitEvent{Code: &code}, nil
	})

	evt, err := m.Wait(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 0, *evt.Code)
}

func TestMockWorker_WaitFor_RunAndReturn(t *testing.T) {
	m := worker.NewMockWorker(t)
	code := 0
	m.EXPECT().WaitFor(context.Background(), time.Second).RunAndReturn(func(ctx context.Context, d time.Duration) (worker.ExitEvent, error) {
		return worker.ExitEvent{Code: &code}, nil
	})

	evt, err := m.WaitFor(context.Background(), time.Second)
	assert.NoError(t, err)
	assert.Equal(t, 0, *evt.Code)
}

func TestMockWorker_DuplexPipe_RunAndReturn(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().DuplexPipe().RunAndReturn(func() (io.ReadWriteCloser, error) {
		return nil, assert.AnError
	})

	_, err := m.DuplexPipe()
	assert.ErrorIs(t, err, assert.AnError)
}

func TestMockWorker_ReadPipe_RunAndReturn(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().ReadPipe().RunAndReturn(func() (io.ReadCloser, error) {
		return nil, assert.AnError
	})

	_, err := m.ReadPipe()
	assert.ErrorIs(t, err, assert.AnError)
}

func TestMockWorker_WritePipe_RunAndReturn(t *testing.T) {
	m := worker.NewMockWorker(t)
	m.EXPECT().WritePipe().RunAndReturn(func() (io.WriteCloser, error) {
		return nil, assert.AnError
	})

	_, err := m.WritePipe()
	assert.ErrorIs(t, err, assert.AnError)
}

// TestMockWorker_Run_Callbacks exercises Run() callbacks.
func TestMockWorker_Start_Run(t *testing.T) {
	m := worker.NewMockWorker(t)

	var called bool
	m.EXPECT().Start(context.Background()).
		Run(func(ctx context.Context) {
			called = true
		}).
		Return(nil)

	_ = m.Start(context.Background())
	assert.True(t, called)
}

func TestMockWorker_Stop_Run(t *testing.T) {
	m := worker.NewMockWorker(t)

	var called bool
	m.EXPECT().Stop().
		Run(func() {
			called = true
		}).
		Return(nil)

	_ = m.Stop()
	assert.True(t, called)
}

func TestMockWorker_Wait_Run(t *testing.T) {
	m := worker.NewMockWorker(t)

	code := 0
	var called bool
	m.EXPECT().Wait(context.Background()).
		Run(func(ctx context.Context) {
			called = true
		}).
		Return(worker.ExitEvent{Code: &code}, nil)

	_, _ = m.Wait(context.Background())
	assert.True(t, called)
}

func TestMockWorker_WaitFor_Run(t *testing.T) {
	m := worker.NewMockWorker(t)

	code := 0
	var called bool
	m.EXPECT().WaitFor(context.Background(), time.Second).
		Run(func(ctx context.Context, d time.Duration) {
			called = true
		}).
		Return(worker.ExitEvent{Code: &code}, nil)

	_, _ = m.WaitFor(context.Background(), time.Second)
	assert.True(t, called)
}

// MARK: - helpers

func newProcessWorkerForTest(t *testing.T) *worker.ProcessWorker {
	t.Helper()
	return worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())
}

package worker_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
	"github.com/lambda-feedback/shimmy/util"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestWorker_Start_IsAlive(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	err := w.Start(context.Background(), worker.StartConfig{Cmd: "cat"})
	assert.NoError(t, err)

	defer w.Kill()

	time.Sleep(1000 * time.Millisecond)

	assert.Equal(t, true, util.IsProcessAlive(w.Pid()))
}

func TestWorker_Start_FailsIfStarted(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	startConfig := worker.StartConfig{Cmd: "cat"}

	err := w.Start(context.Background(), startConfig)
	assert.NoError(t, err)

	defer w.Kill()

	err = w.Start(context.Background(), startConfig)
	assert.Error(t, err)
}

func TestWorker_Start_FailsIfContextCancelled(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := w.Start(ctx, worker.StartConfig{Cmd: "cat"})
	assert.Error(t, err)
}

func TestWorker_TerminatesIfContextCancelled(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())

	err := w.Start(ctx, worker.StartConfig{Cmd: "cat"})
	assert.NoError(t, err)

	// cancel the worker context
	cancel()

	evt, err := w.Wait(context.Background())
	assert.NoError(t, err)

	// the process should have been terminated w/ a sigkill in the background
	assert.Equal(t, syscall.SIGKILL, syscall.Signal(*evt.Signal))
	assert.Nil(t, evt.Code)
}

func TestWorker_CapturesStderr(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	err := w.Start(context.Background(), worker.StartConfig{
		Cmd:  "sh",
		Args: []string{"-c", ">&2 echo \"error\""},
	})
	assert.NoError(t, err)

	defer w.Terminate()

	evt, err := w.Wait(context.Background())
	assert.NoError(t, err)

	assert.Equal(t, 0, *evt.Code)
	assert.Equal(t, "error\n", evt.Stderr)
}

func TestWorker_Wait_ReturnsExitEvent(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	err := w.Start(context.Background(), worker.StartConfig{Cmd: "echo"})
	assert.NoError(t, err)

	defer w.Terminate()

	evt, err := w.Wait(context.Background())
	assert.NoError(t, err)

	assert.Equal(t, 0, *evt.Code)
	assert.Nil(t, evt.Signal)
}

func TestWorker_Wait_ReturnsErrorIfContextCancelled(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	err := w.Start(context.Background(), worker.StartConfig{Cmd: "cat"})
	assert.NoError(t, err)

	defer w.Terminate()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = w.Wait(ctx)
	assert.Error(t, err)
}
func TestWorker_Wait_ReturnsErrorIfCalledMultiple(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	err := w.Start(context.Background(), worker.StartConfig{Cmd: "cat"})
	assert.NoError(t, err)

	w.Terminate()

	_, err = w.Wait(context.Background())
	assert.NoError(t, err)

	_, err = w.Wait(context.Background())
	assert.Error(t, err)
}

func TestWorker_WaitFor_ReturnsExitEvent(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	err := w.Start(context.Background(), worker.StartConfig{Cmd: "echo"})
	assert.NoError(t, err)

	defer w.Terminate()

	evt, err := w.WaitFor(context.Background(), 0)
	assert.NoError(t, err)

	assert.Equal(t, 0, *evt.Code)
	assert.Nil(t, evt.Signal)
}

func TestWorker_WaitFor_ReturnsErrorIfTimeout(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	err := w.Start(context.Background(), worker.StartConfig{
		Cmd:  "sleep",
		Args: []string{"1"},
	})
	assert.NoError(t, err)

	defer w.Terminate()

	_, err = w.WaitFor(context.Background(), 100*time.Millisecond)
	assert.Error(t, err)
}

func TestWorker_Kill_KillsProcess(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	err := w.Start(context.Background(), worker.StartConfig{Cmd: "cat"})
	assert.NoError(t, err)

	w.Kill()

	evt, err := w.Wait(context.Background())
	assert.NoError(t, err)

	// the process should have been terminated w/ a sigkill in the background
	assert.Equal(t, syscall.SIGKILL, syscall.Signal(*evt.Signal))
	assert.Nil(t, evt.Code)

	// the process should not be alive
	assert.Equal(t, false, util.IsProcessAlive(w.Pid()))
}

func TestWorker_Terminate_TerminatesProcess(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	err := w.Start(context.Background(), worker.StartConfig{Cmd: "cat"})
	assert.NoError(t, err)

	w.Terminate()

	evt, err := w.Wait(context.Background())
	assert.NoError(t, err)

	// the process should have been terminated w/ a sigterm in the background
	assert.Equal(t, syscall.SIGTERM, syscall.Signal(*evt.Signal))
	assert.Nil(t, evt.Code)

	// the process should not be alive
	assert.Equal(t, false, util.IsProcessAlive(w.Pid()))
}

func TestWorker_Write_WritesToStdin(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	err := w.Start(context.Background(), worker.StartConfig{Cmd: "cat"})
	assert.NoError(t, err)

	defer w.Terminate()

	input := "foobar"

	_, err = io.Copy(w, strings.NewReader(input))
	assert.NoError(t, err)

	w.Close()

	var outputBuf bytes.Buffer
	_, err = io.Copy(&outputBuf, w)
	assert.NoError(t, err)

	assert.Equal(t, input, outputBuf.String())
}

func TestWorker_Read_ReadsFromStdout(t *testing.T) {
	w := worker.NewProcessWorker(zap.NewNop())

	err := w.Start(context.Background(), worker.StartConfig{
		Cmd:  "echo",
		Args: []string{"foobar"},
	})
	assert.NoError(t, err)

	defer w.Terminate()

	var outputBuf bytes.Buffer
	_, err = io.Copy(&outputBuf, w)
	assert.NoError(t, err)

	assert.Equal(t, "foobar\n", outputBuf.String())
}

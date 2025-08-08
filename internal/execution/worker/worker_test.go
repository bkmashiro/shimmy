package worker_test

import (
	"bytes"
	"context"
	"github.com/stretchr/testify/require"
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
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	err := w.Start(context.Background())
	assert.NoError(t, err)

	defer w.Kill()

	assert.Equal(t, true, util.IsProcessAlive(w.Pid()))
}

func TestWorker_Start_FailsIfStarted(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	err := w.Start(context.Background())
	assert.NoError(t, err)

	defer w.Kill()

	err = w.Start(context.Background())
	assert.Error(t, err)
}

func TestWorker_Start_ReturnsErrorIfInvalidCommand(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: ""}, zap.NewNop())

	err := w.Start(context.Background())
	assert.Error(t, err)
}

func TestWorker_Start_FailsIfContextCancelled(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := w.Start(ctx)
	assert.Error(t, err)
}

func TestWorker_TerminatesIfContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	w := worker.NewProcessWorker(ctx, worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	err := w.Start(context.Background())
	assert.NoError(t, err)

	// cancel the worker context
	cancel()

	var evt worker.ExitEvent
	var waitError error
	require.Eventually(t, func() bool {
		evt, waitError = w.Wait(context.Background())
		return waitError == nil && evt.Signal != nil
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, waitError)
	require.NotNil(t, evt)
}

func TestWorker_CapturesStderr(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{
		Cmd:  "sh",
		Args: []string{"-c", ">&2 echo \"error\""},
	}, zap.NewNop())

	err := w.Start(context.Background())
	assert.NoError(t, err)

	defer w.Stop()

	evt, err := w.Wait(context.Background())
	assert.NoError(t, err)

	assert.Equal(t, 0, *evt.Code)
	assert.Equal(t, "error\n", evt.Stderr)
}

func TestWorker_Wait_ReturnsExitEvent(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "echo"}, zap.NewNop())

	err := w.Start(context.Background())
	assert.NoError(t, err)

	defer w.Stop()

	evt, err := w.Wait(context.Background())
	assert.NoError(t, err)

	assert.Equal(t, 0, *evt.Code)
	assert.Nil(t, evt.Signal)
}

func TestWorker_Wait_ReturnsErrorIfContextCancelled(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	err := w.Start(context.Background())
	assert.NoError(t, err)

	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = w.Wait(ctx)
	assert.Error(t, err)
}
func TestWorker_Wait_ReturnsErrorIfCalledMultiple(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	err := w.Start(context.Background())
	assert.NoError(t, err)

	w.Stop()

	_, err = w.Wait(context.Background())
	assert.NoError(t, err)

	_, err = w.Wait(context.Background())
	assert.Error(t, err)
}

func TestWorker_WaitFor_ReturnsExitEvent(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "echo"}, zap.NewNop())

	err := w.Start(context.Background())
	assert.NoError(t, err)

	defer w.Stop()

	evt, err := w.WaitFor(context.Background(), 0)
	assert.NoError(t, err)

	assert.Equal(t, 0, *evt.Code)
	assert.Nil(t, evt.Signal)
}

func TestWorker_WaitFor_ReturnsErrorIfTimeout(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{
		Cmd:  "sleep",
		Args: []string{"1"},
	}, zap.NewNop())

	err := w.Start(context.Background())
	assert.NoError(t, err)

	defer w.Stop()

	_, err = w.WaitFor(context.Background(), 100*time.Millisecond)
	assert.Error(t, err)
}

func TestWorker_Kill_KillsProcess(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	err := w.Start(context.Background())
	assert.NoError(t, err)

	w.Kill()

	var evt worker.ExitEvent
	var waitError error
	require.Eventually(t, func() bool {
		evt, waitError = w.Wait(context.Background())
		return waitError == nil && evt.Signal != nil
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, waitError)
	require.NotNil(t, evt)

	// the process should have been terminated w/ a sigkill in the background
	assert.Equal(t, syscall.SIGKILL, syscall.Signal(*evt.Signal))
	assert.Nil(t, evt.Code)

	// the process should not be alive
	assert.Equal(t, false, util.IsProcessAlive(w.Pid()))
}

func TestWorker_Terminate_TerminatesProcess(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	err := w.Start(context.Background())
	assert.NoError(t, err)

	w.Stop()

	evt, err := w.Wait(context.Background())
	assert.NoError(t, err)

	// the process should have been terminated w/ a sigterm in the background
	assert.Equal(t, syscall.SIGTERM, syscall.Signal(*evt.Signal))
	assert.Nil(t, evt.Code)

	// the process should not be alive
	assert.Equal(t, false, util.IsProcessAlive(w.Pid()))
}

func TestWorker_DuplexPipe_ReturnsErrorIfAlreadyStarted(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	err := w.Start(context.Background())
	assert.NoError(t, err)

	defer w.Stop()

	_, err = w.DuplexPipe()
	assert.Error(t, err)
}

func TestWorker_DuplexPipe_ReturnsReadWriteStream(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	pipe, err := w.DuplexPipe()
	assert.NoError(t, err)
	assert.NotNil(t, pipe)
}

func TestWorker_Write_WritesToStdin(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	pipe, err := w.DuplexPipe()
	assert.NoError(t, err)

	err = w.Start(context.Background())
	assert.NoError(t, err)

	defer w.Stop()

	input := "foobar"

	_, err = io.Copy(pipe, strings.NewReader(input))
	assert.NoError(t, err)

	pipe.Close()

	var outputBuf bytes.Buffer
	_, err = io.Copy(&outputBuf, pipe)
	assert.NoError(t, err)

	assert.Equal(t, input, outputBuf.String())
}

func TestWorker_Read_ReadsFromStdout(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{
		Cmd:  "echo",
		Args: []string{"foobar"},
	}, zap.NewNop())

	readPipe, err := w.ReadPipe()
	assert.NoError(t, err)

	defer readPipe.Close()

	err = w.Start(context.Background())
	assert.NoError(t, err)

	defer w.Stop()

	var outputBuf bytes.Buffer
	_, err = io.Copy(&outputBuf, readPipe)
	assert.NoError(t, err)

	assert.Equal(t, "foobar\n", outputBuf.String())
}

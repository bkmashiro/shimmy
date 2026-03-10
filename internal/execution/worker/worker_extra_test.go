package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/internal/execution/worker"
)

// MARK: - ExitEvent.String()

func TestExitEvent_String_WithCode(t *testing.T) {
	code := 0
	evt := worker.ExitEvent{Code: &code, Stderr: ""}
	s := evt.String()
	assert.Contains(t, s, "code=0")
	assert.Contains(t, s, "signal=(nil)")
}

func TestExitEvent_String_WithNonZeroCode(t *testing.T) {
	code := 137
	evt := worker.ExitEvent{Code: &code, Stderr: "killed\n"}
	s := evt.String()
	assert.Contains(t, s, "code=137")
	assert.Contains(t, s, "killed")
}

func TestExitEvent_String_WithSignal(t *testing.T) {
	sig := 9
	evt := worker.ExitEvent{Signal: &sig, Stderr: ""}
	s := evt.String()
	assert.Contains(t, s, "code=(nil)")
	assert.Contains(t, s, "signal=9")
}

func TestExitEvent_String_NilCodeAndSignal(t *testing.T) {
	evt := worker.ExitEvent{Stderr: "some error\nmore lines\n"}
	s := evt.String()
	assert.Contains(t, s, "code=(nil)")
	assert.Contains(t, s, "signal=(nil)")
	// newlines should be replaced with spaces
	assert.Contains(t, s, "some error more lines")
}

// MARK: - ExitEvent.Success()

func TestExitEvent_Success_NilCode(t *testing.T) {
	evt := worker.ExitEvent{}
	assert.False(t, evt.Success())
}

func TestExitEvent_Success_NonZero(t *testing.T) {
	code := 1
	evt := worker.ExitEvent{Code: &code}
	assert.False(t, evt.Success())
}

// MARK: - WritePipe

func TestWorker_WritePipe_ReturnsWriter(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	writer, err := w.WritePipe()
	assert.NoError(t, err)
	assert.NotNil(t, writer)
}

func TestWorker_WritePipe_ReturnsErrorIfAlreadyStarted(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	err := w.Start(context.Background())
	require.NoError(t, err)
	defer w.Kill()

	_, err = w.WritePipe()
	assert.ErrorIs(t, err, worker.ErrWorkerAlreadyStarted)
}

// MARK: - ReadPipe errors

func TestWorker_ReadPipe_ReturnsErrorIfAlreadyStarted(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "cat"}, zap.NewNop())

	err := w.Start(context.Background())
	require.NoError(t, err)
	defer w.Kill()

	_, err = w.ReadPipe()
	assert.ErrorIs(t, err, worker.ErrWorkerAlreadyStarted)
}

// MARK: - Pid edge cases

func TestWorker_Pid_BeforeStart(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "echo"}, zap.NewNop())
	assert.Equal(t, 0, w.Pid())
}

// MARK: - Stop/Kill before Start

func TestWorker_Stop_BeforeStart(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "echo"}, zap.NewNop())

	err := w.Stop()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestWorker_Kill_BeforeStart(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "echo"}, zap.NewNop())

	err := w.Kill()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

// MARK: - Non-zero exit code

func TestWorker_Wait_NonZeroExitCode(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{
		Cmd:  "sh",
		Args: []string{"-c", "exit 42"},
	}, zap.NewNop())

	err := w.Start(context.Background())
	require.NoError(t, err)

	evt, err := w.Wait(context.Background())
	assert.NoError(t, err)
	require.NotNil(t, evt.Code)
	assert.Equal(t, 42, *evt.Code)
	assert.Nil(t, evt.Signal)
}

func TestWorker_Wait_NonZeroExitCodeWithStderr(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{
		Cmd:  "sh",
		Args: []string{"-c", ">&2 echo 'failure'; exit 1"},
	}, zap.NewNop())

	err := w.Start(context.Background())
	require.NoError(t, err)

	evt, err := w.Wait(context.Background())
	assert.NoError(t, err)
	require.NotNil(t, evt.Code)
	assert.Equal(t, 1, *evt.Code)
	assert.Contains(t, evt.Stderr, "failure")
}

// MARK: - WaitFor with positive deadline

func TestWorker_WaitFor_WithPositiveDeadline(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{Cmd: "echo"}, zap.NewNop())

	err := w.Start(context.Background())
	require.NoError(t, err)

	evt, err := w.WaitFor(context.Background(), 5*time.Second)
	assert.NoError(t, err)
	require.NotNil(t, evt.Code)
	assert.Equal(t, 0, *evt.Code)
}

// MARK: - ExitEvent.String() edge cases

func TestExitEvent_String_EmptyStderr(t *testing.T) {
	code := 0
	evt := worker.ExitEvent{Code: &code, Stderr: ""}
	s := evt.String()
	assert.Contains(t, s, "stderr=")
}

func TestExitEvent_String_StderrWithTrailingSpaces(t *testing.T) {
	code := 1
	evt := worker.ExitEvent{Code: &code, Stderr: "  error  \n  "}
	s := evt.String()
	// newlines replaced with spaces, then trimmed
	assert.Contains(t, s, "error")
}

// MARK: - WritePipe end-to-end

func TestWorker_WritePipe_WritesToStdin(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{
		Cmd:  "sh",
		Args: []string{"-c", "read line; echo $line"},
	}, zap.NewNop())

	writer, err := w.WritePipe()
	require.NoError(t, err)

	reader, err := w.ReadPipe()
	require.NoError(t, err)

	err = w.Start(context.Background())
	require.NoError(t, err)
	defer w.Kill()

	_, err = writer.Write([]byte("hello\n"))
	require.NoError(t, err)
	writer.Close()

	buf := make([]byte, 256)
	n, _ := reader.Read(buf)
	assert.Equal(t, "hello\n", string(buf[:n]))
}

// MARK: - Start with working directory

func TestWorker_Start_WithCwd(t *testing.T) {
	w := worker.NewProcessWorker(context.Background(), worker.StartConfig{
		Cmd:  "pwd",
		Cwd:  "/tmp",
		Args: nil,
	}, zap.NewNop())

	reader, err := w.ReadPipe()
	require.NoError(t, err)

	err = w.Start(context.Background())
	require.NoError(t, err)

	buf := make([]byte, 256)
	n, _ := reader.Read(buf)
	output := string(buf[:n])
	// /tmp may resolve to /private/tmp on macOS
	assert.Contains(t, output, "tmp")
}

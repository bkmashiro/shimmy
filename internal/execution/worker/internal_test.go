package worker

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MARK: - getExitEvent

func TestGetExitEvent_NilError(t *testing.T) {
	evt := getExitEvent(nil, "")
	require.NotNil(t, evt.Code)
	assert.Equal(t, 0, *evt.Code)
	assert.Nil(t, evt.Signal)
}

func TestGetExitEvent_NilErrorWithStderr(t *testing.T) {
	evt := getExitEvent(nil, "some output")
	require.NotNil(t, evt.Code)
	assert.Equal(t, 0, *evt.Code)
	assert.Equal(t, "some output", evt.Stderr)
}

func TestGetExitEvent_NonExitError(t *testing.T) {
	// A generic error (not *exec.ExitError) should default to code=1
	evt := getExitEvent(errors.New("something went wrong"), "stderr text")
	require.NotNil(t, evt.Code)
	assert.Equal(t, 1, *evt.Code)
	assert.Nil(t, evt.Signal)
	assert.Equal(t, "stderr text", evt.Stderr)
}

func TestGetExitEvent_ExitError(t *testing.T) {
	// Run a command that exits with code 2 to get a real ExitError
	cmd := exec.Command("sh", "-c", "exit 2")
	err := cmd.Run()
	require.Error(t, err)

	evt := getExitEvent(err, "exit 2 stderr")
	require.NotNil(t, evt.Code)
	assert.Equal(t, 2, *evt.Code)
	assert.Nil(t, evt.Signal)
	assert.Equal(t, "exit 2 stderr", evt.Stderr)
}

// MARK: - iostream

func TestIostream_ReadWriteClose(t *testing.T) {
	// Use pipes to simulate stdin/stdout
	cmd := exec.Command("cat")
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)

	err = cmd.Start()
	require.NoError(t, err)
	defer cmd.Process.Kill()

	stream := &iostream{stdout: stdout, stdin: stdin}

	// Write
	n, err := stream.Write([]byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, 4, n)

	// Close (closes stdin, so cat will finish)
	err = stream.Close()
	assert.NoError(t, err)

	// Read
	buf := make([]byte, 256)
	n, err = stream.Read(buf)
	assert.NoError(t, err)
	assert.Equal(t, "test", string(buf[:n]))
}

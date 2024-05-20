package worker

import (
	"context"
	"os/exec"
	"testing"

	"github.com/lambda-feedback/shimmy/util"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestProc_Start_IsAlive(t *testing.T) {
	p, err := startProc(context.Background(), StartConfig{Cmd: "cat"}, zap.NewNop())
	assert.NoError(t, err)

	defer p.Terminate()

	// the process should be started
	assert.Equal(t, true, util.IsProcessAlive(p.Pid()))
}

func TestProc_Wait_WaitsForProcessToExit(t *testing.T) {
	p, err := startProc(context.Background(), StartConfig{Cmd: "echo"}, zap.NewNop())
	assert.NoError(t, err)

	err = p.Wait()
	assert.NoError(t, err)

	// the process should be started
	assert.Equal(t, false, util.IsProcessAlive(p.Pid()))
}

func TestProc_Terminate_SendsTerminationSignal(t *testing.T) {
	p, err := startProc(context.Background(), StartConfig{Cmd: "cat"}, zap.NewNop())
	assert.NoError(t, err)

	err = p.Terminate()
	assert.NoError(t, err)

	err = p.Wait()
	assert.Error(t, err)

	if err, ok := err.(*exec.ExitError); ok {
		// -1 means the process was killed by a signal
		assert.Equal(t, -1, err.ExitCode())
	} else {
		t.Fatal("unexpected error")
	}

	// the process should be killed
	assert.Equal(t, false, util.IsProcessAlive(p.Pid()))
}

func TestProc_ExitsWithFailure_ReturnsError(t *testing.T) {
	p, err := startProc(context.Background(), StartConfig{
		Cmd:  "sh",
		Args: []string{"-c", "exit 1"},
	}, zap.NewNop())
	assert.NoError(t, err)

	err = p.Wait()
	assert.Error(t, err)

	if err, ok := err.(*exec.ExitError); ok {
		assert.Equal(t, 1, err.ExitCode())
	} else {
		t.Fatal("unexpected error")
	}
}

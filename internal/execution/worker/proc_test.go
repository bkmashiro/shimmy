package worker

import (
	"os/exec"
	"testing"
	"time"

	"github.com/lambda-feedback/shimmy/util"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestProc_Start_IsAlive(t *testing.T) {
	p, err := startProc(StartConfig{
		Cmd: "cat",
	}, zap.NewNop())
	assert.NoError(t, err)

	defer p.Terminate(1 * time.Second)

	// give the process some time to start
	time.Sleep(10 * time.Millisecond)

	// the process should be started
	assert.Equal(t, true, util.IsProcessAlive(p.pid))
}

func TestProc_Terminate_KillsProcess(t *testing.T) {
	p, err := startProc(StartConfig{
		Cmd: "cat",
	}, zap.NewNop())
	assert.NoError(t, err)

	p.Terminate(1 * time.Second)

	// give the process some time to terminate
	time.Sleep(10 * time.Millisecond)

	// the process should be killed
	assert.Equal(t, false, util.IsProcessAlive(p.pid))
}

func TestProc_Terminate_SendsTerminationSignal(t *testing.T) {
	p, err := startProc(StartConfig{
		Cmd: "cat",
	}, zap.NewNop())
	assert.NoError(t, err)

	p.Terminate(-1)

	select {
	case <-time.After(1 * time.Second):
		t.Fatal("timeout")
	case err := <-p.termination:
		if err, ok := err.(*exec.ExitError); ok {
			// -1 means the process was killed by a signal
			assert.Equal(t, -1, err.ExitCode())
		} else {
			t.Fatal("unexpected error")
		}
	}

	// the process should be killed
	assert.Equal(t, false, util.IsProcessAlive(p.pid))
}

func TestProc_ExitsWithFailure_ReturnsError(t *testing.T) {
	p, err := startProc(StartConfig{
		Cmd:  "sh",
		Args: []string{"-c", "exit 1"},
	}, zap.NewNop())
	assert.NoError(t, err)

	select {
	case <-time.After(1 * time.Second):
		t.Fatal("timeout")
	case err := <-p.termination:
		assert.NotNil(t, err)
	}
}

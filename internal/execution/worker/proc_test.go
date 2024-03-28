package worker

import (
	"context"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestProc_Start_IsAlive(t *testing.T) {
	p, err := startProc[any, any](StartConfig{
		Cmd:  "sleep",
		Args: []string{"10"},
	}, zap.NewNop())
	assert.NoError(t, err)

	defer p.Terminate(1 * time.Second)

	// give the process some time to start
	time.Sleep(10 * time.Millisecond)

	// the process should be started
	assert.Equal(t, true, isProcessAlive(p.pid))
}

func TestProc_Terminate_KillsProcess(t *testing.T) {
	p, err := startProc[any, any](StartConfig{
		Cmd:  "sleep",
		Args: []string{"10"},
	}, zap.NewNop())
	assert.NoError(t, err)

	p.Terminate(1 * time.Second)

	// give the process some time to terminate
	time.Sleep(10 * time.Millisecond)

	// the process should be killed
	assert.Equal(t, false, isProcessAlive(p.pid))
}

func TestProc_Terminate_SendsTerminationSignal(t *testing.T) {
	p, err := startProc[any, any](StartConfig{
		Cmd:  "sleep",
		Args: []string{"10"},
	}, zap.NewNop())
	assert.NoError(t, err)

	p.Terminate(0)

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
	assert.Equal(t, false, isProcessAlive(p.pid))
}

func TestProc_Write_WritesToStdin(t *testing.T) {
	p, err := startProc[string, string](StartConfig{
		Cmd: "cat",
	}, zap.NewNop())
	assert.NoError(t, err)

	defer p.Terminate(0)

	// write data
	msgId, err := p.Write(context.Background(), "foo")
	assert.NoError(t, err)

	// close stdin
	p.Close()

	msg, err := p.Read(context.Background(), 0)
	assert.NoError(t, err)

	assert.Equal(t, msgId, msg.ID)
	assert.Equal(t, "foo", msg.Data)
}

func isProcessAlive(pid int) bool {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid))

	err := cmd.Run()
	if err, ok := err.(*exec.ExitError); ok {
		// if the process is found ps returns with exit status 0,
		// otherwise it returns with another exit status
		return err.ProcessState.ExitCode() == 0
	}
	if err != nil {
		// if an error occured, return false
		return false
	}

	// ps returned a zero exit status, so the process was found
	return true
}

//go:build linux

package qemurun

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitVMCommandKillsVMWhenRunnerDies(t *testing.T) {
	command := exec.Command("true")
	initVMCommand(command)

	require.NotNil(t, command.SysProcAttr)
	require.True(t, command.SysProcAttr.Setpgid)
	require.Equal(t, syscall.SIGKILL, command.SysProcAttr.Pdeathsig)
}

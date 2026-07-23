//go:build !windows

package qemurun

import (
	"os/exec"
	"syscall"
)

func initVMCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateVMProcess(command *exec.Cmd, force bool) error {
	if command == nil || command.Process == nil {
		return nil
	}
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	if pgid, err := syscall.Getpgid(command.Process.Pid); err == nil {
		return syscall.Kill(-pgid, signal)
	}
	return command.Process.Signal(signal)
}

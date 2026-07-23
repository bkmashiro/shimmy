//go:build windows

package qemurun

import "os/exec"

func initVMCommand(*exec.Cmd) {}

func terminateVMProcess(command *exec.Cmd, _ bool) error {
	if command == nil || command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}

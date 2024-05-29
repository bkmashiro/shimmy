//go:build aix || darwin || dragonfly || freebsd || (js && wasm) || linux || nacl || netbsd || openbsd || solaris

package worker

import (
	"os/exec"
	"syscall"
)

func (p *ProcessWorker) killProcess(force bool) error {
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	if pgid, err := syscall.Getpgid(p.cmd.Process.Pid); err == nil {
		// Negative pid sends signal to all in process group
		return syscall.Kill(-pgid, signal)
	} else {
		return syscall.Kill(p.cmd.Process.Pid, signal)
	}
}

func initCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}

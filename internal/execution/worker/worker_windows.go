package worker

import "os/exec"

func (p *ProcessWorker) killProcess(_ bool) error {
	return p.cmd.Process.Kill()
}

func initCmd(cmd *exec.Cmd) {
	// No-op on Windows.
}

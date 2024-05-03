package worker

import (
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"

	"go.uber.org/zap"
)

type proc struct {
	pid         int
	termination chan error
	stdout      io.ReadCloser
	stderr      io.ReadCloser
	stdin       io.WriteCloser

	log *zap.Logger
}

func startProc(config StartConfig, log *zap.Logger) (*proc, error) {
	cmd := exec.Command(config.Cmd, config.Args...)

	if config.Env != nil {
		env := make([]string, 0, len(config.Env))
		for k, v := range config.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
	}

	if config.Cwd != "" {
		cmd.Dir = config.Cwd
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err = cmd.Start()
	if err != nil {
		return nil, err
	}

	log = log.Named("proc").With(zap.Int("pid", cmd.Process.Pid))

	process := &proc{
		pid:         cmd.Process.Pid,
		termination: make(chan error),
		stdout:      stdout,
		stderr:      stderr,
		stdin:       stdin,
		log:         log,
	}

	go func() {
		// block until the process exits
		err := cmd.Wait()

		// report the exit error to the caller
		process.termination <- err

		// close the termination channel
		close(process.termination)
	}()

	return process, nil
}

func (p *proc) Terminate(timeout time.Duration) error {
	// terminate should report success if the process terminated
	// by the time supervisor receives the request.
	select {
	case <-p.termination:
		p.log.Debug("process already terminated")
		return nil
	default:
		// continue
	}

	p.kill(syscall.SIGTERM)

	return p.waitForTermination(timeout)
}

func (p *proc) Kill(timeout time.Duration) error {
	// kill should report success if the process terminated by the time
	// supervisor receives the request.
	select {
	case <-p.termination:
		p.log.Debug("process already terminated")
		return nil
	default:
		// continue
	}

	// kill the process
	p.kill(syscall.SIGKILL)

	return p.waitForTermination(timeout)
}

func (p *proc) Wait() error {
	return p.waitForTermination(0)
}

func (p *proc) WaitFor(timeout time.Duration) error {
	return p.waitForTermination(timeout)
}

func (p *proc) waitForTermination(timeout time.Duration) error {
	// if timeout is < 0, don't wait for the process to exit
	if timeout < 0 {
		return nil
	}

	// if timeout is 0, wait indefinitely
	if timeout == 0 {
		<-p.termination
		return nil
	}

	// block until either:
	//  * the main process exits (parent ctx is cancelled)
	//  * the child process exits (w.termination is closed)
	//  * the timeout is reached
	select {
	case <-p.termination:
		return nil
	case <-time.After(timeout):
		return ErrKillTimeout
	}
}

func (p *proc) kill(signal syscall.Signal) {
	log := p.log.With(zap.Stringer("signal", signal))

	// close stdin before killing the process, to
	// avoid the process hanging on input
	if err := p.stdin.Close(); err != nil {
		log.Error("close stdin failed", zap.Error(err))
	}

	log.Info("sending signal")

	// best effort, ignore errors
	if err := p.sendKillSignal(signal); err != nil {
		log.Error("stop failed", zap.Error(err))
	}
}

func (p *proc) sendKillSignal(signal syscall.Signal) error {
	if pgid, err := syscall.Getpgid(p.pid); err == nil {
		// Negative pid sends signal to all in process group
		return syscall.Kill(-pgid, signal)
	} else {
		return syscall.Kill(p.pid, signal)
	}
}

// Close closes the stdin pipe of the process.
func (p *proc) Close() error {
	if err := p.stdin.Close(); err != nil {
		return err
	}

	return nil
}

// StdinPipe returns a pipe that will be connected to the
// command's standard input when the command starts.
func (p *proc) StdinPipe() io.WriteCloser {
	return p.stdin
}

// StdoutPipe returns a pipe that will be connected to the
// command's standard output when the command starts.
func (p *proc) StdoutPipe() io.ReadCloser {
	return p.stdout
}

// StderrPipe returns a pipe that will be connected to the
// command's standard error when the command starts.
func (p *proc) StderrPipe() io.ReadCloser {
	return p.stderr
}

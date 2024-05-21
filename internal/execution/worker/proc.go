package worker

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"

	"go.uber.org/zap"
)

type proc struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr io.ReadCloser
	stdin  io.WriteCloser

	log *zap.Logger
}

func startProc(
	ctx context.Context,
	config StartConfig,
	log *zap.Logger,
) (*proc, error) {
	log = log.Named("proc")

	// start process w/ context, so the process is SIGKILL'd when
	// the context is cancelled. This ensures we don't have zombie
	// processes when normal termination fails.
	cmd := exec.CommandContext(ctx, config.Cmd, config.Args...)

	env := os.Environ()
	if config.Env != nil {
		env = append(env, config.Env...)
	}
	cmd.Env = env

	if config.Cwd != "" {
		cmd.Dir = config.Cwd
	}

	// TODO: we open all pipes here. make sure to read from all of them,
	// as we could run into deadlocks otherwise, if the system's stdout
	// or stderr buffers run full.

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

	log.With(
		zap.Strings("args", cmd.Args),
		zap.String("cwd", cmd.Dir),
		zap.Strings("env", cmd.Environ()),
	).Debug("starting process")

	if err = cmd.Start(); err != nil {
		return nil, err
	}

	process := &proc{
		cmd:    cmd,
		stdout: stdout,
		stderr: stderr,
		stdin:  stdin,
		log:    log.With(zap.Int("pid", cmd.Process.Pid)),
	}

	return process, nil
}

func (p *proc) Terminate() error {
	return p.halt(syscall.SIGTERM)
}

func (p *proc) Kill() error {
	return p.halt(syscall.SIGKILL)
}

func (p *proc) halt(signal syscall.Signal) error {
	if p.cmd == nil || p.cmd.Process == nil {
		return errors.New("process is not running")
	}

	log := p.log.With(zap.Stringer("signal", signal))

	// close stdin before killing the process, to
	// avoid the process hanging on input
	if err := p.stdin.Close(); err != nil {
		log.Warn("close stdin failed", zap.Error(err))
	}

	// best effort, ignore errors
	if err := p.sendKillSignal(signal); err != nil {
		log.Warn("sending signal failed", zap.Error(err))
	}

	return nil
}

// Wait blocks until the process exits. The method returns
// an error if the process exits with a non-zero exit code.
func (p *proc) Wait() error {
	if p.cmd == nil {
		return errors.New("process is not running")
	}

	return p.cmd.Wait()
}

func (p *proc) sendKillSignal(signal syscall.Signal) error {
	if pgid, err := syscall.Getpgid(p.cmd.Process.Pid); err == nil {
		// Negative pid sends signal to all in process group
		return syscall.Kill(-pgid, signal)
	} else {
		return syscall.Kill(p.cmd.Process.Pid, signal)
	}
}

// Close closes the stdin pipe of the process.
func (p *proc) Close() error {
	if err := p.stdin.Close(); err != nil {
		return err
	}

	return nil
}

// Pid returns the process ID of the running process.
func (p *proc) Pid() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}

	return p.cmd.Process.Pid
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

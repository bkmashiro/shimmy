package qemurun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type vmDependencies struct {
	buildCommand func(VMConfig) (*exec.Cmd, error)
	dial         func(context.Context, string) (net.Conn, error)
}

type VM struct {
	command         *exec.Cmd
	connection      net.Conn
	wait            <-chan error
	shutdownTimeout time.Duration
	workDir         string
	closeOnce       sync.Once
	closeErr        error
}

func StartVM(ctx context.Context, config RuntimeConfig) (*VM, error) {
	return startVM(ctx, config, vmDependencies{
		buildCommand: BuildQEMUCommand,
		dial: func(ctx context.Context, path string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	})
}

func startVM(ctx context.Context, config RuntimeConfig, deps vmDependencies) (*VM, error) {
	if deps.buildCommand == nil || deps.dial == nil {
		return nil, errors.New("qemu runner: incomplete VM dependencies")
	}
	if err := os.MkdirAll(config.WorkRoot, 0o700); err != nil {
		return nil, fmt.Errorf("qemu runner: create work root: %w", err)
	}
	workDir, err := os.MkdirTemp(config.WorkRoot, "vm-")
	if err != nil {
		return nil, fmt.Errorf("qemu runner: create VM work directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(workDir) }

	vmConfig := config.VM
	vmConfig.ControlSocket = filepath.Join(workDir, "control.sock")
	command, err := deps.buildCommand(vmConfig)
	if err != nil {
		cleanup()
		return nil, err
	}
	stderr := newTailBuffer(64 << 10)
	command.Stdout = io.Discard
	command.Stderr = stderr
	initVMCommand(command)
	if err := command.Start(); err != nil {
		cleanup()
		return nil, fmt.Errorf("qemu runner: start VM: %w", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()

	bootTimeout := config.BootTimeout
	if bootTimeout <= 0 {
		bootTimeout = 60 * time.Second
	}
	bootCtx, cancel := context.WithTimeout(ctx, bootTimeout)
	defer cancel()
	var lastDialError error
	for {
		connection, dialErr := deps.dial(bootCtx, vmConfig.ControlSocket)
		if dialErr == nil {
			return &VM{
				command:         command,
				connection:      connection,
				wait:            wait,
				shutdownTimeout: config.ShutdownTimeout,
				workDir:         workDir,
			}, nil
		}
		lastDialError = dialErr

		select {
		case waitErr := <-wait:
			cleanup()
			return nil, vmExitError(command, waitErr, stderr.String())
		case <-bootCtx.Done():
			_ = terminateVMProcess(command, false)
			select {
			case <-wait:
			case <-time.After(config.ShutdownTimeout):
				_ = terminateVMProcess(command, true)
				<-wait
			}
			cleanup()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, fmt.Errorf("%w: last control socket error: %v", ErrVMBootTimeout, lastDialError)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (v *VM) Connection() net.Conn {
	return v.connection
}

func (v *VM) Close(ctx context.Context) error {
	v.closeOnce.Do(func() {
		if v.connection != nil {
			_ = v.connection.Close()
		}
		_ = terminateVMProcess(v.command, false)
		timeout := v.shutdownTimeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-v.wait:
		case <-ctx.Done():
			_ = terminateVMProcess(v.command, true)
			<-v.wait
			v.closeErr = ctx.Err()
		case <-timer.C:
			_ = terminateVMProcess(v.command, true)
			<-v.wait
		}
		if err := os.RemoveAll(v.workDir); err != nil && v.closeErr == nil {
			v.closeErr = fmt.Errorf("qemu runner: clean VM work directory: %w", err)
		}
	})
	return v.closeErr
}

func vmExitError(command *exec.Cmd, waitErr error, stderr string) error {
	code := -1
	if command.ProcessState != nil {
		code = command.ProcessState.ExitCode()
	}
	return fmt.Errorf("qemu runner: VM exited with code %d before control channel became ready: %s: %w", code, stderr, waitErr)
}

type tailBuffer struct {
	mu   sync.Mutex
	max  int
	data []byte
}

func newTailBuffer(max int) *tailBuffer {
	return &tailBuffer{max: max}
}

func (b *tailBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(data)
	if b.max <= 0 {
		return written, nil
	}
	if len(data) >= b.max {
		b.data = append(b.data[:0], data[len(data)-b.max:]...)
		return written, nil
	}
	overflow := len(b.data) + len(data) - b.max
	if overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, data...)
	return written, nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.data...))
}

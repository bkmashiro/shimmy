package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lambda-feedback/shimmy/internal/execution/qemurun"
)

type vmSession interface {
	Connection() net.Conn
	Close(context.Context) error
}

type runnerDependencies struct {
	loadRuntime func() (qemurun.RuntimeConfig, error)
	startVM     func(context.Context, qemurun.RuntimeConfig) (vmSession, error)
	stdout      io.Writer
}

func defaultRunnerDependencies() runnerDependencies {
	return runnerDependencies{
		loadRuntime: func() (qemurun.RuntimeConfig, error) {
			return qemurun.LoadRuntimeConfig(os.Getenv, qemurun.KVMAvailable())
		},
		startVM: func(ctx context.Context, config qemurun.RuntimeConfig) (vmSession, error) {
			return qemurun.StartVM(ctx, config)
		},
		stdout: os.Stdout,
	}
}

func runRunner(ctx context.Context, args, effectiveEnv []string, dependencies runnerDependencies) (returnErr error) {
	invocation, err := qemurun.ParseInvocation(args, effectiveEnv)
	if err != nil {
		return err
	}
	if dependencies.loadRuntime == nil || dependencies.startVM == nil {
		return errors.New("qemu runner: incomplete dependencies")
	}
	runtimeConfig, err := dependencies.loadRuntime()
	if err != nil {
		return err
	}
	vm, err := dependencies.startVM(ctx, runtimeConfig)
	if err != nil {
		return err
	}
	defer func() {
		closeTimeout := runtimeConfig.ShutdownTimeout + time.Second
		if closeTimeout <= 0 {
			closeTimeout = 11 * time.Second
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		if closeErr := vm.Close(closeCtx); closeErr != nil && returnErr == nil {
			returnErr = closeErr
		}
	}()

	switch invocation.Start.Mode {
	case qemurun.ModeFile:
		request, err := readHostRequest(invocation.HostRequestPath, runtimeConfig.MaxFrameBytes)
		if err != nil {
			return err
		}
		result, _, err := qemurun.RunFile(ctx, vm.Connection(), invocation.Start, request, runtimeConfig.MaxFrameBytes)
		if dependencies.stdout != nil && result.Stdout != "" {
			if _, writeErr := io.WriteString(dependencies.stdout, result.Stdout); writeErr != nil && err == nil {
				return fmt.Errorf("qemu runner: forward evaluator stdout: %w", writeErr)
			}
		}
		if err != nil {
			return err
		}
		return writeHostResponse(invocation.HostResponsePath, result.Response)
	case qemurun.ModeRPC:
		return errors.New("qemu runner: rpc bridge is not wired yet")
	default:
		return fmt.Errorf("qemu runner: unsupported mode %q", invocation.Start.Mode)
	}
}

func readHostRequest(path string, maxFrameBytes int) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("qemu runner: stat host request %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("qemu runner: host request %q is not a regular file", path)
	}
	if info.Size()+5 > int64(maxFrameBytes) {
		return nil, fmt.Errorf("qemu runner: host request %q exceeds frame limit", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("qemu runner: read host request %q: %w", path, err)
	}
	return data, nil
}

func writeHostResponse(path string, data []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("qemu runner: stat host response %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("qemu runner: host response %q is not a regular file", path)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("qemu runner: open host response %q: %w", path, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("qemu runner: write host response %q: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("qemu runner: close host response %q: %w", path, err)
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runRunner(ctx, os.Args[1:], os.Environ(), defaultRunnerDependencies()); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		code := 1
		var remoteExit *qemurun.RemoteExitError
		if errors.As(err, &remoteExit) && remoteExit.Code > 0 && remoteExit.Code < 126 {
			code = remoteExit.Code
		}
		os.Exit(code)
	}
}

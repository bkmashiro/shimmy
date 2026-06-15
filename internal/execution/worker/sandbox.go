//go:build linux

package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"go.uber.org/zap"
)

// NewSandboxedWorkerFactory returns a WorkerFactoryFn that wraps each worker
// process with nsjail. It is a drop-in replacement for defaultWorkerFactory.
// Returns an error if the nsjail binary cannot be found at cfg.NsjailPath.
func NewSandboxedWorkerFactory(cfg SandboxConfig) (func(context.Context, StartConfig, *zap.Logger) (Worker, error), error) {
	if cfg.NsjailPath == "" {
		cfg.NsjailPath = "/usr/sbin/nsjail"
	}

	if _, err := os.Stat(cfg.NsjailPath); err != nil {
		return nil, fmt.Errorf("nsjail binary not found at %q: %w", cfg.NsjailPath, err)
	}

	return func(ctx context.Context, config StartConfig, log *zap.Logger) (Worker, error) {
		sandboxed, err := applySandbox(config, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to build sandboxed config: %w", err)
		}
		return NewProcessWorker(ctx, sandboxed, log), nil
	}, nil
}

// applySandbox rewrites config so that nsjail wraps the original command.
func applySandbox(config StartConfig, cfg SandboxConfig) (StartConfig, error) {
	if config.Cmd == "" {
		return StartConfig{}, errors.New("cannot sandbox empty command")
	}

	return StartConfig{
		Cmd: cfg.NsjailPath,
		// CWD is managed by --cwd inside nsjail; exec.Cmd CWD is irrelevant.
		Cwd:  "",
		Args: buildNsjailArgs(config, cfg),
		Env:  config.Env,
	}, nil
}

// buildNsjailArgs constructs the full nsjail argument list for wrapping config.Cmd.
func buildNsjailArgs(config StartConfig, cfg SandboxConfig) []string {
	var args []string

	// Exec mode: run the command directly with inherited stdio.
	// Use 'e' (execve), not 'o' (once/TCP) — we rely on stdio, not a network socket.
	args = append(args, "--mode", "e")

	// Suppress nsjail's own log output so it doesn't pollute worker stderr.
	args = append(args, "--log", "/dev/null")

	// Drop privileges: run worker as nobody unless overridden.
	user := cfg.User
	if user == "" {
		user = "65534:65534"
	}
	args = append(args, "--user", user)

	// Use the host root as the jail root; bind mounts control what's visible.
	args = append(args, "--chroot", "/")

	// Preserve the worker's intended working directory inside the sandbox.
	if config.Cwd != "" {
		args = append(args, "--cwd", config.Cwd)
	}

	// Filesystem: read-only bind mounts.
	for _, path := range cfg.ReadOnlyBinds {
		args = append(args, "--bindmount_ro", path)
	}

	// Filesystem: read-write bind mounts.
	for _, path := range cfg.WritableBinds {
		args = append(args, "--bindmount", path)
	}

	// Filesystem: tmpfs mounts.
	for _, path := range cfg.TmpfsMounts {
		args = append(args, "--tmpfsmount", path)
	}

	// Network: by default keep the host network namespace.
	// --disable_clone_newnet avoids creating a new (empty) network namespace.
	if cfg.DisableNetwork {
		args = append(args, "--iface_no_lo")
	} else {
		args = append(args, "--disable_clone_newnet")
	}

	// When running as root (e.g. inside a privileged container), skip user
	// namespace creation — nested CLONE_NEWUSER is typically blocked by the
	// container runtime. setuid/setgid via --user still drops privileges.
	if os.Getuid() == 0 {
		args = append(args, "--disable_clone_newuser")
	}

	// Resource limits.
	// Use --rlimit_cpu (kernel RLIMIT_CPU) rather than --time_limit (nsjail's
	// wall-clock monitor), which requires cgroupv2 and silently does nothing
	// when cgroups are unavailable (e.g. inside a container).
	if cfg.CpuTimeLimit > 0 {
		args = append(args, "--rlimit_cpu", strconv.Itoa(cfg.CpuTimeLimit))
	}
	if cfg.MemoryLimit > 0 {
		// rlimit_as is in MiB when passed to nsjail.
		args = append(args, "--rlimit_as", strconv.Itoa(cfg.MemoryLimit))
	}
	if cfg.MaxFds > 0 {
		args = append(args, "--rlimit_nofile", strconv.Itoa(cfg.MaxFds))
	}

	// Seccomp: use nsjail's built-in default syscall policy.
	if cfg.Seccomp {
		args = append(args, "--seccomp_default_policy=1")
	}

	// Separator: everything after "--" is the command to execute.
	args = append(args, "--")
	args = append(args, config.Cmd)
	args = append(args, config.Args...)

	return args
}

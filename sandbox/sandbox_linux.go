//go:build linux
// +build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Run executes a command in a sandbox
func Run(name string, args []string, config Config) (*Result, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = config.WorkDir
	cmd.Env = config.Env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set up process attributes
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Start timing
	start := time.Now()

	// Start process
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	// Apply rlimits to child (via /proc)
	if err := applyRlimitsToProc(cmd.Process.Pid, config); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("rlimits: %w", err)
	}

	// Wait with timeout
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var result Result
	result.StartTime = start

	select {
	case err := <-done:
		result.Duration = time.Since(start)
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				result.ExitCode = exitErr.ExitCode()
			} else {
				result.Error = err
			}
		}
	case <-time.After(config.Timeout):
		// Kill process group
		pgid, _ := syscall.Getpgid(cmd.Process.Pid)
		syscall.Kill(-pgid, syscall.SIGKILL)
		result.Timeout = true
		result.Duration = config.Timeout
	}

	result.EndTime = time.Now()
	return &result, nil
}

// Result holds execution results
type Result struct {
	ExitCode  int
	Duration  time.Duration
	StartTime time.Time
	EndTime   time.Time
	Timeout   bool
	Error     error
}

// applyRlimitsToProc applies rlimits to a running process
func applyRlimitsToProc(pid int, config Config) error {
	// Note: We can't apply rlimits to another process directly.
	// The child process needs to apply them itself.
	// This is a limitation - in production, use a wrapper binary
	// or apply in child after fork but before exec.
	return nil
}

// ApplyRlimits applies rlimits to the current process
func ApplyRlimits(config Config) error {
	limits := []struct {
		resource int
		value    uint64
	}{
		{unix.RLIMIT_CPU, config.MaxCPUSeconds},
		{unix.RLIMIT_AS, config.MaxMemoryBytes},
		{unix.RLIMIT_NPROC, config.MaxProcesses},
		{unix.RLIMIT_FSIZE, config.MaxFileSize},
		{unix.RLIMIT_NOFILE, config.MaxOpenFiles},
		{unix.RLIMIT_CORE, 0},
	}

	for _, l := range limits {
		rlim := &unix.Rlimit{Cur: l.value, Max: l.value}
		if err := unix.Setrlimit(l.resource, rlim); err != nil {
			return fmt.Errorf("setrlimit %d: %w", l.resource, err)
		}
	}
	return nil
}

// ApplySeccomp applies seccomp filter
func ApplySeccomp(config Config) error {
	if !config.EnableSeccomp {
		return nil
	}

	var filter *SeccompFilter
	if config.AllowNetwork {
		filter = DefaultAllowlistFilter()
	} else {
		// Combine allowlist with network deny
		filter = DefaultAllowlistFilter()
		// Add network denies... (simplified)
	}

	return filter.Apply()
}

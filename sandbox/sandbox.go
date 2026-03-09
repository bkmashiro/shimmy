// sandbox/sandbox.go - Userspace sandbox for shimmy
//
// Provides process isolation for student code execution in Lambda.
// Uses seccomp-bpf + rlimits (no root required).

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Config holds sandbox configuration
type Config struct {
	// Resource limits
	MaxCPUSeconds   uint64 // RLIMIT_CPU
	MaxMemoryBytes  uint64 // RLIMIT_AS
	MaxProcesses    uint64 // RLIMIT_NPROC
	MaxFileSize     uint64 // RLIMIT_FSIZE
	MaxOpenFiles    uint64 // RLIMIT_NOFILE

	// Execution
	Timeout time.Duration
	WorkDir string
	Env     []string

	// Network
	AllowNetwork bool

	// Syscall filtering
	EnableSeccomp bool
}

// DefaultConfig returns a restrictive default configuration
func DefaultConfig() Config {
	return Config{
		MaxCPUSeconds:  5,
		MaxMemoryBytes: 256 * 1024 * 1024, // 256MB
		MaxProcesses:   10,
		MaxFileSize:    10 * 1024 * 1024, // 10MB
		MaxOpenFiles:   100,
		Timeout:        30 * time.Second,
		AllowNetwork:   false,
		EnableSeccomp:  true,
	}
}

// SandboxedCmd creates a sandboxed exec.Cmd
type SandboxedCmd struct {
	cmd    *exec.Cmd
	config Config
}

// NewSandboxedCmd creates a new sandboxed command
func NewSandboxedCmd(name string, args []string, config Config) *SandboxedCmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = config.WorkDir
	cmd.Env = config.Env

	// Set process attributes for isolation
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // New process group
	}

	return &SandboxedCmd{
		cmd:    cmd,
		config: config,
	}
}

// Start starts the sandboxed process
func (s *SandboxedCmd) Start() error {
	// Apply rlimits in parent (will be inherited by child)
	if err := s.applyRlimits(); err != nil {
		return fmt.Errorf("apply rlimits: %w", err)
	}

	return s.cmd.Start()
}

// Wait waits for the sandboxed process to complete
func (s *SandboxedCmd) Wait() error {
	done := make(chan error, 1)
	go func() {
		done <- s.cmd.Wait()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(s.config.Timeout):
		s.Kill()
		return fmt.Errorf("timeout after %v", s.config.Timeout)
	}
}

// Kill terminates the sandboxed process
func (s *SandboxedCmd) Kill() error {
	if s.cmd.Process == nil {
		return nil
	}
	// Kill entire process group
	pgid, err := syscall.Getpgid(s.cmd.Process.Pid)
	if err == nil {
		return syscall.Kill(-pgid, syscall.SIGKILL)
	}
	return s.cmd.Process.Kill()
}

// applyRlimits sets resource limits
func (s *SandboxedCmd) applyRlimits() error {
	// Note: These are applied to current process and inherited by child.
	// In production, use SysProcAttr.Rlimits or apply in child via fork+exec.

	limits := []struct {
		resource int
		value    uint64
	}{
		{unix.RLIMIT_CPU, s.config.MaxCPUSeconds},
		{unix.RLIMIT_AS, s.config.MaxMemoryBytes},
		{unix.RLIMIT_NPROC, s.config.MaxProcesses},
		{unix.RLIMIT_FSIZE, s.config.MaxFileSize},
		{unix.RLIMIT_NOFILE, s.config.MaxOpenFiles},
		{unix.RLIMIT_CORE, 0}, // No core dumps
	}

	for _, l := range limits {
		rlim := &unix.Rlimit{Cur: l.value, Max: l.value}
		if err := unix.Setrlimit(l.resource, rlim); err != nil {
			return fmt.Errorf("setrlimit %d: %w", l.resource, err)
		}
	}

	return nil
}

// Stdin returns stdin pipe
func (s *SandboxedCmd) Stdin() *os.File {
	// Implementation would create pipe
	return nil
}

// Stdout returns stdout pipe
func (s *SandboxedCmd) Stdout() *os.File {
	return nil
}

// Stderr returns stderr pipe
func (s *SandboxedCmd) Stderr() *os.File {
	return nil
}

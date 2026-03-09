// Package sandbox provides process isolation for student code execution.
package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Config defines sandbox restrictions
type Config struct {
	MaxCPUSeconds  uint64
	MaxMemoryMB    uint64
	MaxProcesses   uint64
	MaxFileSizeMB  uint64
	MaxOpenFiles   uint64
	Timeout        time.Duration
	AllowNetwork   bool
	WrapperPath    string
}

// DefaultConfig returns a restrictive default configuration
func DefaultConfig() Config {
	return Config{
		MaxCPUSeconds:  5,
		MaxMemoryMB:    256,
		MaxProcesses:   10,
		MaxFileSizeMB:  10,
		MaxOpenFiles:   100,
		Timeout:        30 * time.Second,
		AllowNetwork:   false,
		WrapperPath:    "sandbox-wrapper",
	}
}

// WrapCommand wraps a command with sandbox restrictions
func WrapCommand(name string, args []string, config Config) *exec.Cmd {
	wrapperArgs := buildArgs(config)
	wrapperArgs = append(wrapperArgs, "--", name)
	wrapperArgs = append(wrapperArgs, args...)
	return exec.Command(config.WrapperPath, wrapperArgs...)
}

// WrapCommandContext wraps a command with context support
func WrapCommandContext(ctx context.Context, name string, args []string, config Config) *exec.Cmd {
	wrapperArgs := buildArgs(config)
	wrapperArgs = append(wrapperArgs, "--", name)
	wrapperArgs = append(wrapperArgs, args...)
	return exec.CommandContext(ctx, config.WrapperPath, wrapperArgs...)
}

func buildArgs(c Config) []string {
	args := []string{
		fmt.Sprintf("-cpu=%d", c.MaxCPUSeconds),
		fmt.Sprintf("-mem=%d", c.MaxMemoryMB),
		fmt.Sprintf("-nproc=%d", c.MaxProcesses),
		fmt.Sprintf("-fsize=%d", c.MaxFileSizeMB),
		fmt.Sprintf("-nofile=%d", c.MaxOpenFiles),
		fmt.Sprintf("-timeout=%s", c.Timeout),
	}
	if !c.AllowNetwork {
		args = append(args, "--no-network")
	}
	return args
}

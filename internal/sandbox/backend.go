package sandbox

import (
	"context"
	"os/exec"
)

// SandboxBackend defines how a process is wrapped by a sandbox runtime.
type SandboxBackend interface {
	WrapCommand(ctx context.Context, name string, args []string, cfg Config) (*exec.Cmd, error)
	Available() bool
	Name() string
}

// Config defines sandbox limits and execution context.
type Config struct {
	MaxMemoryMB  int
	CPUTimeSecs  int
	AllowNetwork bool
	WorkDir      string
	EnvVars      []string
}

// DefaultConfig returns conservative defaults for untrusted code.
func DefaultConfig() Config {
	return Config{
		MaxMemoryMB:  256,
		CPUTimeSecs:  10,
		AllowNetwork: false,
	}
}

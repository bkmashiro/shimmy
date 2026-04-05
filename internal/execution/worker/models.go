package worker

import (
	"fmt"
)

var (
	ErrWorkerAlreadyStarted = fmt.Errorf("worker already started")
)

type SandboxConfig struct {
	Enabled       bool   `conf:"enabled"`
	BinaryPath    string `conf:"binary_path"`
	MemoryMB      int    `conf:"memory_mb"`
	MaxProcs      int    `conf:"max_procs"`
	MaxFSizeMB    int    `conf:"max_fsize_mb"`
	MaxFDs        int    `conf:"max_fds"`
	NoNetwork     bool   `conf:"no_network"`
	OutputLimitKB int    `conf:"output_limit_kb"`
	Backend       string `conf:"backend"`
}

type StartConfig struct {
	// Cmd is the path or name of the binary to execute
	Cmd string `conf:"cmd"`

	// Cwd is the working directory in which
	// the binary should be executed
	Cwd string `conf:"cwd"`

	// Args is the list of arguments to pass to the command
	Args []string `conf:"arg"`

	// Env is a map of environment variables
	// to set when running the command
	Env []string `conf:"env"`

	Sandbox SandboxConfig `conf:"sandbox"`
}

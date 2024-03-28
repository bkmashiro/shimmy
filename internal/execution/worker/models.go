package worker

import (
	"fmt"
	"time"
)

var (
	ErrKillTimeout          = fmt.Errorf("kill timeout")
	ErrInvalidTimeout       = fmt.Errorf("invalid timeout")
	ErrWorkerNotStarted     = fmt.Errorf("worker not started")
	ErrWorkerAlreadyStarted = fmt.Errorf("worker already started")
)

type StartConfig struct {
	// Cmd is the path or name of the binary to execute
	Cmd string `conf:"cmd"`

	// Cwd is the working directory in which
	// the binary should be executed
	Cwd string `conf:"cwd"`

	// Args is the list of arguments to pass to the command
	Args []string `conf:"args"`

	// Env is a map of environment variables
	// to set when running the command
	Env map[string]string `conf:"env"`
}

type StopConfig struct {
	// Timeout is the duration to wait for the worker to stop
	Timeout time.Duration `conf:"timeout"`
}

type SendConfig struct {
	// Timeout is the duration to wait for the worker to send a message
	Timeout time.Duration `conf:"timeout"`
}

type ReadConfig struct {
	// Timeout is the duration to wait for the worker to send a message
	Timeout time.Duration `conf:"timeout"`
}

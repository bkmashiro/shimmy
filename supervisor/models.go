package supervisor

import "time"

type StartParams struct {
	// Cmd is the path or name of the binary to execute
	Cmd string

	// Cwd is the working directory in which
	// the binary should be executed
	Cwd string

	// Args is the list of arguments to pass to the command
	Args []string

	// Env is a map of environment variables
	// to set when running the command
	Env map[string]any
}

type StopParams struct {
	// Timeout is the duration to wait for the worker to stop
	Timeout time.Duration
}

type SendParams struct {
	// Timeout is the duration to wait for the worker to send a message
	Timeout time.Duration
}

type ReadParams struct {
	// Timeout is the duration to wait for the worker to send a message
	Timeout time.Duration
}

type IOMode string

const (
	// StdIO describes communication over stdin/stdout
	StdIO IOMode = "stdio"

	// FileIO describes communication w/ processes over files
	FileIO IOMode = "file"
)

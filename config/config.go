package config

import "github.com/lambda-feedback/shimmy/runtime"

type MessageEncoding string

const (
	JSON MessageEncoding = "json"
)

type Config struct {
	// LogLevel is the log level for the application
	LogLevel string `conf:"log_level"`

	// LogFormat is the log format for the application
	LogFormat string `conf:"log_format"`

	// LogOutput is the log output for the application
	// Interface string `conf:"interface"`

	// Command is the command to execute
	// Command string `conf:"command"`

	// Args are the arguments to pass to the command
	// Args []string `conf:"arg"`

	// Disposable indicates if the command terminates after one message
	// Disposable bool `conf:"disposable"`

	// Encoding is the encoding to use for the message
	// Encoding MessageEncoding `conf:"encoding"`

	// MaxProcs is the maximum number of processes to run concurrently
	// MaxProcs int `conf:"max_procs"`

	// Runtime is the runtime configuration
	Runtime runtime.Config `conf:"runtime"`
}

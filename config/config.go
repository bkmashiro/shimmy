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

	// Runtime is the runtime configuration
	Runtime runtime.Config `conf:"runtime"`
}

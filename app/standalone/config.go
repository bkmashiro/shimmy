package standalone

import "github.com/lambda-feedback/shimmy/internal/server"

type Config struct {
	// HttpConfig represents the configuration for the HTTP server.
	HttpConfig server.HttpConfig `conf:",squash"`
}

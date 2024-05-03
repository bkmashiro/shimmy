package runtime

import (
	"strings"
)

// Command is a command that can be sent between the runtime and the handler.
type Command string

const (
	// CommandPreview is the command to get a preview for the response.
	CommandPreview Command = "preview"

	// CommandEvaluate is the command to evaluate the response.
	CommandEvaluate Command = "eval"
)

// ParseCommand parses a command from a given path.
func ParseCommand(path string) (Command, bool) {
	switch strings.ToLower(path) {
	case "eval":
		return CommandEvaluate, true
	case "preview":
		return CommandPreview, true
	}

	return "", false
}

type Message map[string]any

// NewRequestMessage creates a new message.
func NewRequestMessage(command Command, data map[string]any) Message {
	data["command"] = command
	return data
}

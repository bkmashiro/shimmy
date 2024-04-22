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
	CommandEvaluate Command = "evaluate"
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

// Message is a message that can be sent between the runtime and the handler.
type Message struct {
	Command Command `json:"command,omitempty"`
	Data    []byte  `json:"data"`
}

// NewMessage creates a new message.
func NewMessage(command Command, data []byte) Message {
	return Message{
		Command: command,
		Data:    data,
	}
}

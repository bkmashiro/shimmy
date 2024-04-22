package runtime

import (
	"github.com/lambda-feedback/shimmy/models"
)

// Message is a message that can be sent between the runtime and the handler.
type Message struct {
	Command models.Command `json:"command,omitempty"`
	Data    []byte         `json:"data"`
}

// NewMessage creates a new message.
func NewMessage(
	command models.Command,
	content []byte,
) Message {
	return Message{
		Command: command,
		Data:    content,
	}
}

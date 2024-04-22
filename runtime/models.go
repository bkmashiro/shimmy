package runtime

import (
	"github.com/lambda-feedback/shimmy/models"
)

type Message struct {
	Command models.Command `json:"command,omitempty"`
	Data    []byte         `json:"data"`
}

func NewMessage(
	command models.Command,
	content []byte,
) Message {
	return Message{
		Command: command,
		Data:    content,
	}
}

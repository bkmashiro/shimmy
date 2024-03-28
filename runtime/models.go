package runtime

import "strings"

type Command int

const (
	PreviewCommand Command = iota
	EvaluateCommand
)

func ParseCommand(path string) (Command, bool) {
	switch strings.ToLower(path) {
	case "evaluate":
		return EvaluateCommand, true
	case "preview":
		return PreviewCommand, true
	}

	return 0, false
}

type Message struct {
	Command Command        `json:"command,omitempty"`
	Data    any            `json:"data"`
	Params  map[string]any `json:"params"`
}

func NewMessage(
	command Command,
	content any,
	meta map[string]any,
) Message {
	return Message{
		Command: command,
		Data:    content,
		Params:  meta,
	}
}

func NewPreviewMessage(data string, meta map[string]any) Message {
	return NewMessage(PreviewCommand, data, nil)
}

func NewEvaluateMessage(data string, meta map[string]any) Message {
	return NewMessage(EvaluateCommand, data, nil)
}

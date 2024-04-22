package models

import (
	"strings"
)

type Command string

const (
	CommandPreview  Command = "preview"
	CommandEvaluate Command = "evaluate"
)

func ParseCommand(path string) (Command, bool) {
	switch strings.ToLower(path) {
	case "evaluate":
		return CommandEvaluate, true
	case "preview":
		return CommandPreview, true
	}

	return "", false
}

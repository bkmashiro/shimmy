package models

import (
	"strings"
)

type Command string

const (
	CommandPreview  Command = "preview"
	CommandEvaluate Command = "eval"
)

func ParseCommand(path string) (Command, bool) {
	switch strings.ToLower(path) {
	case "eval":
		return CommandEvaluate, true
	case "preview":
		return CommandPreview, true
	}

	return "", false
}

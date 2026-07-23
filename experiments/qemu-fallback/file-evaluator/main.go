package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type requestEnvelope struct {
	Command string          `json:"command"`
	Params  json.RawMessage `json:"params"`
}

type responseEnvelope struct {
	Command string         `json:"command"`
	Result  resultEnvelope `json:"result"`
}

type resultEnvelope struct {
	IsCorrect bool            `json:"is_correct"`
	Echo      json.RawMessage `json:"echo"`
}

func evaluate(request []byte) ([]byte, error) {
	var envelope requestEnvelope
	if err := json.Unmarshal(request, &envelope); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	if envelope.Command == "" {
		return nil, errors.New("request command is empty")
	}
	if len(envelope.Params) == 0 {
		envelope.Params = json.RawMessage(`{}`)
	}
	response, err := json.Marshal(responseEnvelope{
		Command: envelope.Command,
		Result: resultEnvelope{
			IsCorrect: true,
			Echo:      envelope.Params,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	return response, nil
}

func run(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: file-evaluator [args...] request.json response.json")
	}
	requestPath := args[len(args)-2]
	responsePath := args[len(args)-1]
	request, err := os.ReadFile(requestPath)
	if err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	response, err := evaluate(request)
	if err != nil {
		return err
	}
	if err := os.WriteFile(responsePath, response, 0o600); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

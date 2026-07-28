package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/lambda-feedback/shimmy/experiments/runtime-comparison-bridge/contract"
)

type requestEnvelope struct {
	Command string `json:"command"`
	Params  struct {
		Response string            `json:"response"`
		Answer   string            `json:"answer"`
		Params   contract.Workload `json:"params"`
	} `json:"params"`
}

type responseEnvelope struct {
	Command string          `json:"command"`
	Result  contract.Result `json:"result"`
}

func evaluate(request []byte) ([]byte, error) {
	var envelope requestEnvelope
	if err := json.Unmarshal(request, &envelope); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	if envelope.Command != "eval" {
		return nil, fmt.Errorf("unsupported command %q", envelope.Command)
	}
	result, err := contract.Evaluate(envelope.Params.Response, envelope.Params.Answer, envelope.Params.Params, 1)
	if err != nil {
		return nil, err
	}
	return json.Marshal(responseEnvelope{Command: "eval", Result: result})
}

func run(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: native-file [args...] request.json response.json")
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

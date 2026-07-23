package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const maxRPCFixtureFrameBytes = 4 << 20

type rpcRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      json.RawMessage   `json:"id"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type evaluatorResponse struct {
	Command string          `json:"command"`
	Result  evaluatorResult `json:"result"`
}

type evaluatorResult struct {
	IsCorrect bool            `json:"is_correct"`
	Echo      json.RawMessage `json:"echo"`
}

func serveOne(input *bufio.Reader, output io.Writer) error {
	payload, err := readLSPFrame(input)
	if err != nil {
		return err
	}
	var request rpcRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return writeRPCError(output, nil, -32700, "parse error")
	}
	response := rpcResponse{JSONRPC: "2.0", ID: request.ID}
	if request.JSONRPC != "2.0" || len(request.ID) == 0 {
		response.Error = &rpcError{Code: -32600, Message: "invalid request"}
	} else if request.Method != "eval" {
		response.Error = &rpcError{Code: -32601, Message: "method not found"}
	} else {
		echo := json.RawMessage(`{}`)
		if len(request.Params) > 0 && len(request.Params[0]) > 0 {
			echo = request.Params[0]
		}
		response.Result = evaluatorResponse{
			Command: "eval",
			Result:  evaluatorResult{IsCorrect: true, Echo: echo},
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return writeLSPFrame(output, encoded)
}

func writeRPCError(output io.Writer, id json.RawMessage, code int, message string) error {
	encoded, err := json.Marshal(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	})
	if err != nil {
		return err
	}
	return writeLSPFrame(output, encoded)
}

func readLSPFrame(input *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := input.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			break
		}
		key, value, found := strings.Cut(line, ":")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			continue
		}
		if contentLength >= 0 {
			return nil, errors.New("duplicate Content-Length")
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 0 || parsed > maxRPCFixtureFrameBytes {
			return nil, errors.New("invalid Content-Length")
		}
		contentLength = parsed
	}
	if contentLength < 0 {
		return nil, errors.New("missing Content-Length")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(input, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeLSPFrame(output io.Writer, payload []byte) error {
	if len(payload) > maxRPCFixtureFrameBytes {
		return errors.New("RPC fixture response exceeds frame limit")
	}
	if _, err := fmt.Fprintf(output, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	for len(payload) > 0 {
		count, err := output.Write(payload)
		if count > 0 {
			payload = payload[count:]
		}
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func run(input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	for {
		if err := serveOne(reader, output); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

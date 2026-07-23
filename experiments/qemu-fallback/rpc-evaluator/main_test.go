package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

func TestServeOneReturnsSchemaValidJSONRPCResponse(t *testing.T) {
	request := []byte(`{"jsonrpc":"2.0","id":7,"method":"eval","params":[{"response":"same","answer":"same"}]}`)
	var input bytes.Buffer
	writeFrameForTest(t, &input, request)
	var output bytes.Buffer
	if err := serveOne(bufio.NewReader(&input), &output); err != nil {
		t.Fatalf("serveOne: %v", err)
	}
	payload := readFrameForTest(t, bufio.NewReader(&output))
	var response map[string]any
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response["jsonrpc"] != "2.0" || response["id"].(float64) != 7 {
		t.Fatalf("response envelope = %#v", response)
	}
	result := response["result"].(map[string]any)
	if result["command"] != "eval" {
		t.Fatalf("result = %#v", result)
	}
	body := result["result"].(map[string]any)
	if body["is_correct"] != true || body["echo"].(map[string]any)["answer"] != "same" {
		t.Fatalf("body = %#v", body)
	}
}

func TestServeOneRejectsUnknownMethodWithJSONRPCError(t *testing.T) {
	request := []byte(`{"jsonrpc":"2.0","id":8,"method":"unknown","params":[]}`)
	var input bytes.Buffer
	writeFrameForTest(t, &input, request)
	var output bytes.Buffer
	if err := serveOne(bufio.NewReader(&input), &output); err != nil {
		t.Fatalf("serveOne: %v", err)
	}
	payload := readFrameForTest(t, bufio.NewReader(&output))
	var response map[string]any
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response["error"] == nil {
		t.Fatalf("response = %#v", response)
	}
}

func writeFrameForTest(t *testing.T, output *bytes.Buffer, payload []byte) {
	t.Helper()
	if err := writeLSPFrame(output, payload); err != nil {
		t.Fatal(err)
	}
}

func readFrameForTest(t *testing.T, input *bufio.Reader) []byte {
	t.Helper()
	payload, err := readLSPFrame(input)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

package main

import (
	"encoding/json"
	"testing"
)

func TestEvaluateReturnsCommandAndParamsDeterministically(t *testing.T) {
	request := []byte(`{"command":"eval","params":{"answer":"42","nested":{"ok":true}}}`)
	response, err := evaluate(request)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(response, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["command"] != "eval" {
		t.Fatalf("command = %#v", got["command"])
	}
	result, ok := got["result"].(map[string]any)
	echo, echoOK := result["echo"].(map[string]any)
	if !ok || !echoOK || result["is_correct"] != true || echo["answer"] != "42" {
		t.Fatalf("result = %#v", got["result"])
	}
}

func TestEvaluateRejectsMalformedRequest(t *testing.T) {
	if _, err := evaluate([]byte(`{"command":`)); err == nil {
		t.Fatal("evaluate accepted malformed JSON")
	}
}

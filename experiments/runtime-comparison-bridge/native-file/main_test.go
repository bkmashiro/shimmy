package main

import (
	"encoding/json"
	"testing"
)

func TestEvaluateFileEnvelope(t *testing.T) {
	request := []byte(`{"command":"eval","params":{"response":"42","answer":"42","params":{"iterations":100000,"seed":7}}}`)
	response, err := evaluate(request)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	var decoded responseEnvelope
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Command != "eval" || !decoded.Result.IsCorrect {
		t.Fatalf("unexpected response: %+v", decoded)
	}
	if decoded.Result.WorkChecksum != "5e7135fac6225d57" || decoded.Result.GuestInvocationCount != 1 {
		t.Fatalf("unexpected result: %+v", decoded.Result)
	}
}

func TestEvaluateRejectsUnknownCommand(t *testing.T) {
	if _, err := evaluate([]byte(`{"command":"preview","params":{}}`)); err == nil {
		t.Fatal("unknown command should fail")
	}
}

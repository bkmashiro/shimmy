package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorkerCallPayloadDoesNotDuplicateLargeResponseInAnswer(t *testing.T) {
	response := map[string]any{"payload": string(make([]byte, 1<<20))}
	payload := workerCallPayload(response, map[string]any{"seed": 7})

	assert.Equal(t, response, payload["response"])
	assert.Equal(t, "expected", payload["answer"])
	assert.NotEqual(t, response, payload["answer"])
}

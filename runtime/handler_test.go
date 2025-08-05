package runtime_test

import (
	"context"
	"encoding/json"
	"github.com/lambda-feedback/shimmy/runtime"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"net/http"
	"testing"
)

var correctFeedback = map[string]any{
	"command": "eval",
	"result": map[string]interface{}{
		"is_correct": true,
		"feedback":   "Well done! Your answer is correct.",
	},
}

// mockRuntime implements the runtime.Runtime interface.
type mockRuntime struct{}

func (m *mockRuntime) Handle(ctx context.Context, request runtime.EvaluationRequest) (runtime.EvaluationResponse, error) {
	// Example response
	return correctFeedback, nil
}

func (m *mockRuntime) Start(ctx context.Context) error {
	//TODO Not required for tests
	panic("Not required")
}

func (m *mockRuntime) Shutdown(ctx context.Context) error {
	//TODO Not required for tests
	panic("Not required")
}

func TestRuntimeHandler_Handle_Success(t *testing.T) {
	log := zaptest.NewLogger(t)

	// Create the runtime handler with mockRuntime
	handler, err := runtime.NewRuntimeHandler(runtime.HandlerParams{
		Runtime: &mockRuntime{},
		Log:     log,
	})
	require.NoError(t, err)

	// Request body that matches the request schema
	body := map[string]any{
		"response": 1,
		"answer":   1,
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	req := runtime.Request{
		Method: http.MethodPost,
		Path:   "/eval",
		Body:   bodyBytes,
		Header: http.Header{
			"command": []string{"eval"},
		},
	}

	resp := handler.Handle(context.Background(), req)

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var respBody map[string]any
	err = json.Unmarshal(resp.Body, &respBody)
	require.NoError(t, err)
	require.Equal(t, correctFeedback, respBody)
}

func TestRuntimeHandler_Handle_InvalidCommand(t *testing.T) {
	log := zaptest.NewLogger(t)

	handler, err := runtime.NewRuntimeHandler(runtime.HandlerParams{
		Runtime: &mockRuntime{},
		Log:     log,
	})
	require.NoError(t, err)

	// Use an invalid command that will fail to parse
	req := runtime.Request{
		Method: http.MethodPost,
		Path:   "/!invalid", // Will trigger ParseCommand failure
		Body:   []byte(`{}`),
		Header: http.Header{},
	}

	resp := handler.Handle(context.Background(), req)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRuntimeHandler_Handle_InvalidMethod(t *testing.T) {
	log := zaptest.NewLogger(t)

	handler, err := runtime.NewRuntimeHandler(runtime.HandlerParams{
		Runtime: &mockRuntime{},
		Log:     log,
	})
	require.NoError(t, err)

	req := runtime.Request{
		Method: http.MethodGet, // Not allowed
		Path:   "/eval",
		Body:   []byte(`{}`),
		Header: http.Header{},
	}

	resp := handler.Handle(context.Background(), req)
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

package runtime_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/runtime"
)

// TestParseCommand_AllCommands exercises all valid command strings.
func TestParseCommand_AllCommands(t *testing.T) {
	tests := []struct {
		input   string
		want    runtime.Command
		wantOK  bool
	}{
		{"eval", runtime.CommandEvaluate, true},
		{"EVAL", runtime.CommandEvaluate, true},
		{"preview", runtime.CommandPreview, true},
		{"PREVIEW", runtime.CommandPreview, true},
		{"healthcheck", runtime.CommandHealth, true},
		{"HEALTHCHECK", runtime.CommandHealth, true},
		{"unknown", "", false},
		{"", "", false},
		{"bad!cmd", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			cmd, ok := runtime.ParseCommand(tc.input)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, cmd)
		})
	}
}

// TestEvaluationRuntime_Handle exercises the EvaluationRuntime.Handle wrapper.
func TestEvaluationRuntime_Handle(t *testing.T) {
	mockRT := new(mockRuntime)

	expected := runtime.EvaluationResponse{
		"command": "eval",
		"result":  map[string]any{"is_correct": true, "feedback": "ok"},
	}

	mockRT.On("Handle", mock.Anything, mock.MatchedBy(func(req runtime.EvaluationRequest) bool {
		return req.Command == runtime.CommandEvaluate
	})).Return(expected, nil)

	handler, err := runtime.NewRuntimeHandler(runtime.HandlerParams{
		Runtime: mockRT,
		Log:     zap.NewNop(),
	})
	require.NoError(t, err)

	body := createRequestBody(t, map[string]any{"response": "x", "answer": "x"})
	req := createRequest("POST", "/eval", body, nil)
	req.Header = make(map[string][]string)
	req.Header.Set("command", "eval")

	resp := handler.Handle(context.Background(), req)
	assert.Equal(t, 200, resp.StatusCode)
}

// TestEvaluationRuntime_Handle_ErrorFromRuntime exercises runtime error propagation.
func TestEvaluationRuntime_Handle_ErrorFromRuntime(t *testing.T) {
	mockRT := new(mockRuntime)
	mockRT.On("Handle", mock.Anything, mock.Anything).Return(runtime.EvaluationResponse{}, assert.AnError)

	handler, err := runtime.NewRuntimeHandler(runtime.HandlerParams{
		Runtime: mockRT,
		Log:     zap.NewNop(),
	})
	require.NoError(t, err)

	body := createRequestBody(t, map[string]any{"response": "x", "answer": "x"})
	req := createRequest("POST", "/eval", body, nil)
	req.Header = make(map[string][]string)
	req.Header.Set("command", "eval")

	resp := handler.Handle(context.Background(), req)
	assert.NotEqual(t, 200, resp.StatusCode)
}

// TestGetErrorStatusCode_ValidationRequestError exercises the validation error path.
func TestRuntimeHandler_Handle_ValidationRequestError(t *testing.T) {
	mockRT := new(mockRuntime)
	// Return a valid-looking response to avoid response validation errors
	mockRT.On("Handle", mock.Anything, mock.Anything).Return(runtime.EvaluationResponse{
		"command": "eval",
		"result": map[string]any{
			"is_correct": true,
			"feedback":   "ok",
		},
	}, nil).Maybe()

	handler, err := runtime.NewRuntimeHandler(runtime.HandlerParams{
		Runtime: mockRT,
		Log:     zap.NewNop(),
	})
	require.NoError(t, err)

	// POST with missing required fields to trigger request validation error
	body := createRequestBody(t, map[string]any{})
	req := createRequest("POST", "/eval", body, nil)
	req.Header = make(map[string][]string)
	req.Header.Set("command", "eval")

	resp := handler.Handle(context.Background(), req)
	// 422 for request validation error
	assert.Equal(t, 422, resp.StatusCode)
}

// TestRuntimeHandler_Handle_InvalidJSON exercises JSON parse error.
func TestRuntimeHandler_Handle_InvalidJSON(t *testing.T) {
	handler, err := runtime.NewRuntimeHandler(runtime.HandlerParams{
		Runtime: &mockRuntime{},
		Log:     zap.NewNop(),
	})
	require.NoError(t, err)

	// POST with invalid JSON body
	req := createRequest("POST", "/eval", []byte("not-json"), nil)
	req.Header = make(map[string][]string)
	req.Header.Set("command", "eval")

	resp := handler.Handle(context.Background(), req)
	assert.NotEqual(t, 200, resp.StatusCode)
}

// TestRuntimeHandler_Handle_InvalidResponseSchema tests when the runtime returns invalid data.
func TestRuntimeHandler_Handle_InvalidResponseSchema(t *testing.T) {
	mockRT := new(mockRuntime)
	// Return response with missing required fields so response validation fails
	mockRT.On("Handle", mock.Anything, mock.Anything).Return(runtime.EvaluationResponse{
		"command": "eval",
		// missing "result" field
	}, nil)

	handler, err := runtime.NewRuntimeHandler(runtime.HandlerParams{
		Runtime: mockRT,
		Log:     zap.NewNop(),
	})
	require.NoError(t, err)

	body := createRequestBody(t, map[string]any{"response": "x", "answer": "x"})
	req := createRequest("POST", "/eval", body, nil)
	req.Header = make(map[string][]string)
	req.Header.Set("command", "eval")

	resp := handler.Handle(context.Background(), req)
	// Response schema validation failure results in non-200
	assert.NotEqual(t, 200, resp.StatusCode)
}

// TestRuntimeHandler_Healthcheck_NoRequestSchema tests healthcheck skips request validation.
func TestRuntimeHandler_Handle_HealthcheckSkipsRequestSchema(t *testing.T) {
	mockRT := new(mockRuntime)
	mockRT.On("Handle", mock.Anything, mock.Anything).Return(runtime.EvaluationResponse{
		"command": "healthcheck",
		"result": map[string]any{
			"tests_passed": true,
			"successes":    []interface{}{},
			"failures":     []interface{}{},
			"errors":       []interface{}{},
		},
	}, nil)

	handler, err := runtime.NewRuntimeHandler(runtime.HandlerParams{
		Runtime: mockRT,
		Log:     zap.NewNop(),
	})
	require.NoError(t, err)

	// Empty body is fine for healthcheck (no request schema)
	body := createRequestBody(t, map[string]any{})
	req := createRequest("POST", "/", body, nil)
	req.Header = make(map[string][]string)
	req.Header.Set("command", "healthcheck")

	resp := handler.Handle(context.Background(), req)
	assert.Equal(t, 200, resp.StatusCode)
}

// TestGetCaseFeedback_MultipleMatches exercises the multiple-matches warning.
func TestRuntimeHandler_Handle_MultipleMatches_Warning(t *testing.T) {
	handler := setupHandlerWithMockFunc(t, mockEvalFunc)

	body := createRequestBody(t, map[string]any{
		"response": "yes",
		"answer":   "world",
		"params": map[string]any{
			"cases": []map[string]any{
				{"answer": "yes", "feedback": "match 1"},
				{"answer": "yes", "feedback": "match 2"},
			},
		},
	})

	req := createRequest("POST", "/eval", body, nil)
	req.Header = make(map[string][]string)
	req.Header.Set("command", "eval")

	resp := handler.Handle(context.Background(), req)
	respBody := parseResponseBody(t, resp)
	result := respBody["result"].(map[string]interface{})

	// Should have a warning about multiple matches
	assert.False(t, result["is_correct"].(bool))
	// matched_case should be set
	assert.Equal(t, float64(0), result["matched_case"])
}

// TestRuntimeHandler_Handle_EvalCase_OverrideFeedbackWithEvalFeedback tests
// the override_eval_feedback logic.
func TestRuntimeHandler_Handle_EvalCase_OverrideFeedbackWithEvalFeedback(t *testing.T) {
	handler := setupHandlerWithMockFunc(t, mockEvalFunc)

	body := createRequestBody(t, map[string]any{
		"response": "other",
		"answer":   "hello",
		"params": map[string]any{
			"cases": []map[string]any{
				{
					"answer":   "other",
					"feedback": "case feedback",
					"params": map[string]any{
						"override_eval_feedback": true,
					},
				},
			},
		},
	})

	req := createRequest("POST", "/eval", body, nil)
	req.Header = make(map[string][]string)
	req.Header.Set("command", "eval")

	resp := handler.Handle(context.Background(), req)
	respBody := parseResponseBody(t, resp)
	result := respBody["result"].(map[string]interface{})

	assert.False(t, result["is_correct"].(bool))
	// feedback should combine case feedback and eval feedback
	feedback, ok := result["feedback"].(string)
	assert.True(t, ok)
	assert.Contains(t, feedback, "case feedback")
}

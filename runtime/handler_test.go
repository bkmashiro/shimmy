package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/lambda-feedback/shimmy/runtime"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"net/http"
	"testing"
)

// mockRuntime implements the runtime.Runtime interface.
type mockRuntime struct {
	mock.Mock
}

func (m *mockRuntime) Handle(ctx context.Context, request runtime.EvaluationRequest) (runtime.EvaluationResponse, error) {
	args := m.Called(ctx, request)

	// If you want to generate a response based on the request dynamically:
	if responseFunc, ok := args.Get(0).(func(runtime.EvaluationRequest) (runtime.EvaluationResponse, error)); ok {
		response, err := responseFunc(request)
		return response, err
	}

	// Otherwise, return the static response
	return args.Get(0).(runtime.EvaluationResponse), args.Error(1)
}

func (m *mockRuntime) Start(ctx context.Context) error {
	//Not required for tests
	panic("Not required")
}

func (m *mockRuntime) Shutdown(ctx context.Context) error {
	//Not required for tests
	panic("Not required")
}

func setupLogger(t *testing.T) *zap.Logger {
	return zaptest.NewLogger(t)
}

func setupHandlerWithStaticMock(t *testing.T, mockResponse runtime.EvaluationResponse) runtime.Handler {
	mockRT := new(mockRuntime)
	mockRT.On("Handle", mock.Anything, mock.Anything).Return(mockResponse, nil)

	handler, err := runtime.NewRuntimeHandler(runtime.HandlerParams{
		Runtime: mockRT,
		Log:     setupLogger(t),
	})
	require.NoError(t, err)

	return handler
}

func setupHandlerWithMockFunc(t *testing.T, mockResponse func(req runtime.EvaluationRequest) (runtime.EvaluationResponse, error)) runtime.Handler {
	mockRT := new(mockRuntime)
	mockRT.On("Handle", mock.Anything, mock.Anything).Return(mockResponse, nil)

	handler, err := runtime.NewRuntimeHandler(runtime.HandlerParams{
		Runtime: mockRT,
		Log:     setupLogger(t),
	})
	require.NoError(t, err)

	return handler
}

func createRequestBody(t *testing.T, body map[string]any) []byte {
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)
	return bodyBytes
}

func createRequest(method, path string, body []byte, header http.Header) runtime.Request {
	return runtime.Request{
		Method: method,
		Path:   path,
		Body:   body,
		Header: header,
	}
}

func parseResponseBody(t *testing.T, resp runtime.Response) map[string]any {
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var respBody map[string]any
	err := json.Unmarshal(resp.Body, &respBody)
	require.NoError(t, err)

	return respBody
}

func TestRuntimeHandler_Handle_Success(t *testing.T) {
	mockResponse := runtime.EvaluationResponse{
		"command": "eval",
		"result": map[string]interface{}{
			"is_correct": true,
			"feedback":   "Well done! Your answer is correct.",
		},
	}

	handler := setupHandlerWithStaticMock(t, mockResponse)

	body := createRequestBody(t, map[string]any{
		"response": 1,
		"answer":   1,
	})

	req := createRequest(http.MethodPost, "/eval", body, http.Header{
		"command": []string{"eval"},
	})

	resp := handler.Handle(context.Background(), req)
	respBody := parseResponseBody(t, resp)

	require.Equal(t, mockResponse["result"], respBody["result"])
}

func TestRuntimeHandler_Handle_InvalidCommand(t *testing.T) {
	handler, err := runtime.NewRuntimeHandler(runtime.HandlerParams{
		Runtime: &mockRuntime{},
		Log:     setupLogger(t),
	})
	require.NoError(t, err)

	req := createRequest(http.MethodPost, "/!invalid", []byte(`{}`), http.Header{})
	resp := handler.Handle(context.Background(), req)

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRuntimeHandler_Handle_InvalidMethod(t *testing.T) {
	handler, err := runtime.NewRuntimeHandler(runtime.HandlerParams{
		Runtime: &mockRuntime{},
		Log:     setupLogger(t),
	})
	require.NoError(t, err)

	req := createRequest(http.MethodGet, "/eval", []byte(`{}`), http.Header{})
	resp := handler.Handle(context.Background(), req)

	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestRuntimeHandler_Handle_Single_Feedback_Case(t *testing.T) {
	mockResponse := runtime.EvaluationResponse{
		"command": "eval",
		"result": map[string]interface{}{
			"is_correct": true,
		},
	}

	handler := setupHandlerWithStaticMock(t, mockResponse)

	body := createRequestBody(t, map[string]any{
		"response": "hello",
		"answer":   "hello",
		"params": map[string]any{
			"cases": []map[string]any{
				{"answer": "other", "feedback": "should be 'hello'."},
			},
		},
	})

	req := createRequest(http.MethodPost, "/eval", body, http.Header{
		"command": []string{"eval"},
	})

	resp := handler.Handle(context.Background(), req)
	result := parseResponseBody(t, resp)["result"].(map[string]interface{})

	require.True(t, result["is_correct"].(bool))
	require.NotContains(t, result, "matched_case")
	require.NotContains(t, result, "feedback")
}

func TestRuntimeHandler_Handle_Single_Feedback_Case_Match(t *testing.T) {
	mockResponse := runtime.EvaluationResponse{
		"command": "eval",
		"result": map[string]interface{}{
			"is_correct":   false,
			"matched_case": 0,
			"feedback":     "should be 'hello'.",
		},
	}

	handler := setupHandlerWithStaticMock(t, mockResponse)

	body := createRequestBody(t, map[string]any{
		"response": "hello",
		"answer":   "hello",
		"params": map[string]any{
			"cases": []map[string]any{
				{"answer": "other", "feedback": "should be 'hello'."},
			},
		},
	})

	req := createRequest(http.MethodPost, "/eval", body, http.Header{
		"command": []string{"eval"},
	})

	resp := handler.Handle(context.Background(), req)
	result := parseResponseBody(t, resp)["result"].(map[string]interface{})

	require.False(t, result["is_correct"].(bool))
	require.Equal(t, float64(0), result["matched_case"])
	require.Equal(t, "should be 'hello'.", result["feedback"])
}

func TestRunTimeHandler_Warning_Data_Structure(t *testing.T) {
	mockResponse := runtime.EvaluationResponse{
		"command": "eval",
		"result": map[string]interface{}{
			"is_correct": false,
			"feedback":   "Missing answer/feedback field",
		},
	}

	handler := setupHandlerWithStaticMock(t, mockResponse)

	body := createRequestBody(t, map[string]any{
		"response": "hello",
		"answer":   "world",
		"params": map[string]any{
			"cases": []map[string]any{
				{"feedback": "should be 'hello'."},
				{"answer": "other", "feedback": "should be 'hello'."},
			},
		},
	})

	req := createRequest(http.MethodPost, "/eval", body, http.Header{
		"command": []string{"eval"},
	})

	resp := handler.Handle(context.Background(), req)
	result := parseResponseBody(t, resp)["result"].(map[string]interface{})

	require.False(t, result["is_correct"].(bool))
	require.Contains(t, result, "warnings")

	warnings := result["warnings"].([]interface{})
	require.Len(t, warnings, 1)
	warningContent := warnings[0].(map[string]interface{})
	require.Equal(t, "Missing answer field", warningContent["message"])
	require.Equal(t, float64(0), warningContent["case"])
}

func TestRuntimeHandler_Handle_Multi_Cases_Single_Match(t *testing.T) {

	mockResponse := func(req runtime.EvaluationRequest) (runtime.EvaluationResponse, error) {
		if req.Data["answer"] == req.Data["response"] {
			return runtime.EvaluationResponse{
				"command": "eval",
				"result": map[string]interface{}{
					"is_correct": true,
					"feedback":   "should be 'yes'.",
				},
			}, nil
		}
		return runtime.EvaluationResponse{
			"command": "eval",
			"result": map[string]interface{}{
				"is_correct": false,
				"feedback":   "should be 'hello'.",
			},
		}, nil
	}

	handler := setupHandlerWithMockFunc(t, mockResponse)

	body := createRequestBody(t, map[string]any{
		"response": "yes",
		"answer":   "world",
		"params": map[string]any{
			"cases": []map[string]any{
				{"answer": "hello", "feedback": "should be 'hello'."},
				{"answer": "yes", "feedback": "should be 'yes'."},
				{"answer": "no", "feedback": "should be 'no'."},
			},
		},
	})

	req := createRequest(http.MethodPost, "/eval", body, http.Header{
		"command": []string{"eval"},
	})

	resp := handler.Handle(context.Background(), req)
	result := parseResponseBody(t, resp)["result"].(map[string]interface{})

	require.False(t, result["is_correct"].(bool))
	require.Equal(t, float64(1), result["matched_case"])
	require.Equal(t, "should be 'yes'.", result["feedback"])
}

func TestRuntimeHandler_Handle_Multi_Cases_Many_Match(t *testing.T) {

	mockResponse := func(req runtime.EvaluationRequest) (runtime.EvaluationResponse, error) {
		if req.Data["answer"] == req.Data["response"] {
			return runtime.EvaluationResponse{
				"command": "eval",
				"result": map[string]interface{}{
					"is_correct": true,
					"feedback":   "should be 'yes'.",
				},
			}, nil
		}
		return runtime.EvaluationResponse{
			"command": "eval",
			"result": map[string]interface{}{
				"is_correct": false,
				"feedback":   "should be 'hello'.",
			},
		}, nil
	}

	handler := setupHandlerWithMockFunc(t, mockResponse)

	body := createRequestBody(t, map[string]any{
		"response": "yes",
		"answer":   "world",
		"params": map[string]any{
			"cases": []map[string]any{
				{"answer": "hello", "feedback": "should be 'hello'."},
				{"answer": "yes", "feedback": "should be 'yes'."},
				{"answer": "yes", "feedback": "should be 'not this one'."},
			},
		},
	})

	req := createRequest(http.MethodPost, "/eval", body, http.Header{
		"command": []string{"eval"},
	})

	resp := handler.Handle(context.Background(), req)
	result := parseResponseBody(t, resp)["result"].(map[string]interface{})

	require.False(t, result["is_correct"].(bool))
	require.Equal(t, float64(1), result["matched_case"])
	require.Equal(t, "should be 'yes'.", result["feedback"])
}

func TestRuntimeHandler_Catch_Exception(t *testing.T) {

	mockResponse := func(req runtime.EvaluationRequest) (runtime.EvaluationResponse, error) {
		if params, ok := req.Data["params"].(map[string]interface{}); ok {
			if raiseVal, ok := params["raise"].(bool); ok && raiseVal {
				return nil, errors.New("catches exception as warning test")
			}
		}

		return runtime.EvaluationResponse{
			"command": "eval",
			"result": map[string]interface{}{
				"is_correct": false,
				"feedback":   "should be 'hello'.",
			},
		}, nil
	}

	handler := setupHandlerWithMockFunc(t, mockResponse)

	body := createRequestBody(t, map[string]any{
		"response": "yes",
		"answer":   "world",
		"params": map[string]any{
			"cases": []map[string]any{
				{
					"answer":   "hello",
					"feedback": "should be 'hello'.",
					"params": map[string]any{
						"raise": true,
					},
				},
			},
		},
	})

	req := createRequest(http.MethodPost, "/eval", body, http.Header{
		"command": []string{"eval"},
	})

	resp := handler.Handle(context.Background(), req)
	result := parseResponseBody(t, resp)["result"].(map[string]interface{})

	require.False(t, result["is_correct"].(bool))
	require.Contains(t, result, "warnings")

	warnings := result["warnings"].([]interface{})
	require.Len(t, warnings, 1)
	warningContent := warnings[0].(map[string]interface{})
	require.Equal(t, "catches exception as warning test", warningContent["message"])
	require.Equal(t, float64(0), warningContent["case"])
}

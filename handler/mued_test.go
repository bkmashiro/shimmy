package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lambda-feedback/shimmy/config"
	"github.com/lambda-feedback/shimmy/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- Mock runtime ---

type MockRuntime struct {
	mock.Mock
}

func (m *MockRuntime) Handle(ctx context.Context, req runtime.EvaluationRequest) (runtime.EvaluationResponse, error) {
	args := m.Called(ctx, req)
	return args.Get(0).(runtime.EvaluationResponse), args.Error(1)
}

func (m *MockRuntime) Start(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockRuntime) Shutdown(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

// --- Helpers ---

func newMuEdHandler(h runtime.Handler, r runtime.Runtime, key string) *MuEdHandler {
	return &MuEdHandler{
		handler: h,
		runtime: r,
		config:  config.Config{Auth: config.AuthConfig{Key: key}},
		log:     zap.NewNop(),
	}
}

func mathEvalBody(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"submission": map[string]any{
			"type":    "MATH",
			"content": map[string]any{"expression": "x^2"},
		},
		"task": map[string]any{
			"referenceSolution": map[string]any{
				"type":    "MATH",
				"content": map[string]any{"expression": "x^2"},
			},
		},
	})
	require.NoError(t, err)
	return b
}

func evalHandlerResponse(isCorrect bool, feedback string) runtime.Response {
	body, _ := json.Marshal(map[string]any{
		"command": "eval",
		"result": map[string]any{
			"is_correct": isCorrect,
			"feedback":   feedback,
		},
	})
	return runtime.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       body,
	}
}

// --- ServeEvaluate tests ---

func TestMuEdServeEvaluate_Success(t *testing.T) {
	mockHandler := new(MockHandler)
	mockHandler.On("Handle", mock.Anything, mock.Anything).
		Return(evalHandlerResponse(true, "Well done"))

	req := httptest.NewRequest(http.MethodPost, "/evaluate", bytes.NewReader(mathEvalBody(t)))
	w := httptest.NewRecorder()

	newMuEdHandler(mockHandler, nil, "").ServeEvaluate(w, req)

	res := w.Result()
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json", res.Header.Get("Content-Type"))

	var feedback []map[string]any
	require.NoError(t, json.Unmarshal(body, &feedback))
	require.Len(t, feedback, 1)
	assert.Equal(t, 1.0, feedback[0]["awardedPoints"])
	assert.Equal(t, "Well done", feedback[0]["message"])
	assert.Contains(t, string(body), `"responseLatex":null`)
	assert.Contains(t, string(body), `"responseSimplified":null`)

	mockHandler.AssertExpectations(t)
}

func TestMuEdServeEvaluate_LegacyBodyForwarded(t *testing.T) {
	mockHandler := new(MockHandler)
	mockHandler.On("Handle", mock.Anything, mock.MatchedBy(func(r runtime.Request) bool {
		var body map[string]any
		if err := json.Unmarshal(r.Body, &body); err != nil {
			return false
		}
		return body["response"] == "x^2" &&
			body["answer"] == "x^2" &&
			r.Header.Get("Command") == "eval"
	})).Return(evalHandlerResponse(true, "Correct"))

	req := httptest.NewRequest(http.MethodPost, "/evaluate", bytes.NewReader(mathEvalBody(t)))
	w := httptest.NewRecorder()

	newMuEdHandler(mockHandler, nil, "").ServeEvaluate(w, req)

	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
	mockHandler.AssertExpectations(t)
}

func TestMuEdServeEvaluate_Preview(t *testing.T) {
	previewBody, _ := json.Marshal(map[string]any{
		"submission": map[string]any{
			"type":    "MATH",
			"content": map[string]any{"expression": "x^2"},
		},
		"preSubmissionFeedback": map[string]any{"enabled": true},
	})

	previewResult := map[string]any{"preview": map[string]any{"latex": "x^{2}"}}
	respBody, _ := json.Marshal(map[string]any{
		"command": "preview",
		"result":  previewResult,
	})
	mockHandler := new(MockHandler)
	mockHandler.On("Handle", mock.Anything, mock.MatchedBy(func(r runtime.Request) bool {
		return r.Header.Get("Command") == "preview"
	})).Return(runtime.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       respBody,
	})

	req := httptest.NewRequest(http.MethodPost, "/evaluate", bytes.NewReader(previewBody))
	w := httptest.NewRecorder()

	newMuEdHandler(mockHandler, nil, "").ServeEvaluate(w, req)

	res := w.Result()
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)

	assert.Equal(t, http.StatusOK, res.StatusCode)

	var feedback []map[string]any
	require.NoError(t, json.Unmarshal(raw, &feedback))
	require.Len(t, feedback, 1)
	assert.NotNil(t, feedback[0]["preSubmissionFeedback"])

	mockHandler.AssertExpectations(t)
}

func TestMuEdServeEvaluate_Unauthorized(t *testing.T) {
	mockHandler := new(MockHandler)

	req := httptest.NewRequest(http.MethodPost, "/evaluate", bytes.NewReader(mathEvalBody(t)))
	req.Header.Set("api-key", "wrong")
	w := httptest.NewRecorder()

	newMuEdHandler(mockHandler, nil, "secret").ServeEvaluate(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Result().StatusCode)
	mockHandler.AssertNotCalled(t, "Handle", mock.Anything, mock.Anything)
}

func TestMuEdServeEvaluate_MethodNotAllowed(t *testing.T) {
	mockHandler := new(MockHandler)

	req := httptest.NewRequest(http.MethodGet, "/evaluate", nil)
	w := httptest.NewRecorder()

	newMuEdHandler(mockHandler, nil, "").ServeEvaluate(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Result().StatusCode)
	mockHandler.AssertNotCalled(t, "Handle", mock.Anything, mock.Anything)
}

func TestMuEdServeEvaluate_InvalidJSON(t *testing.T) {
	mockHandler := new(MockHandler)

	req := httptest.NewRequest(http.MethodPost, "/evaluate", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()

	newMuEdHandler(mockHandler, nil, "").ServeEvaluate(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	mockHandler.AssertNotCalled(t, "Handle", mock.Anything, mock.Anything)
}

func TestMuEdServeEvaluate_MissingReferenceSolution(t *testing.T) {
	mockHandler := new(MockHandler)

	body, _ := json.Marshal(map[string]any{
		"submission": map[string]any{
			"type":    "MATH",
			"content": map[string]any{"expression": "x^2"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/evaluate", bytes.NewReader(body))
	w := httptest.NewRecorder()

	newMuEdHandler(mockHandler, nil, "").ServeEvaluate(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	mockHandler.AssertNotCalled(t, "Handle", mock.Anything, mock.Anything)
}

func TestMuEdServeEvaluate_WorkerErrorForwarded(t *testing.T) {
	errorBody, _ := json.Marshal(map[string]any{
		"error": map[string]any{"message": "evaluation failed"},
	})
	mockHandler := new(MockHandler)
	mockHandler.On("Handle", mock.Anything, mock.Anything).Return(runtime.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       errorBody,
	})

	req := httptest.NewRequest(http.MethodPost, "/evaluate", bytes.NewReader(mathEvalBody(t)))
	w := httptest.NewRecorder()

	newMuEdHandler(mockHandler, nil, "").ServeEvaluate(w, req)

	res := w.Result()
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)

	assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
	assert.Equal(t, errorBody, bytes.TrimRight(raw, "\n"))
}

// --- ServeHealth tests ---

func TestMuEdServeHealth_Success(t *testing.T) {
	healthResult := map[string]any{"tests_passed": true, "successes": []any{}, "failures": []any{}, "errors": []any{}}
	mockRuntime := new(MockRuntime)
	mockRuntime.On("Handle", mock.Anything, runtime.EvaluationRequest{
		Command: runtime.CommandHealth,
		Data:    map[string]any{},
	}).Return(runtime.EvaluationResponse{
		"command": "healthcheck",
		"result":  healthResult,
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/evaluate/health", nil)
	w := httptest.NewRecorder()

	newMuEdHandler(nil, mockRuntime, "").ServeHealth(w, req)

	res := w.Result()
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json", res.Header.Get("Content-Type"))

	var result map[string]any
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Equal(t, true, result["tests_passed"])

	mockRuntime.AssertExpectations(t)
}

func TestMuEdServeHealth_Unauthorized(t *testing.T) {
	mockRuntime := new(MockRuntime)

	req := httptest.NewRequest(http.MethodGet, "/evaluate/health", nil)
	req.Header.Set("api-key", "wrong")
	w := httptest.NewRecorder()

	newMuEdHandler(nil, mockRuntime, "secret").ServeHealth(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Result().StatusCode)
	mockRuntime.AssertNotCalled(t, "Handle", mock.Anything, mock.Anything)
}

func TestMuEdServeHealth_MethodNotAllowed(t *testing.T) {
	mockRuntime := new(MockRuntime)

	req := httptest.NewRequest(http.MethodPost, "/evaluate/health", nil)
	w := httptest.NewRecorder()

	newMuEdHandler(nil, mockRuntime, "").ServeHealth(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Result().StatusCode)
	mockRuntime.AssertNotCalled(t, "Handle", mock.Anything, mock.Anything)
}

func TestMuEdServeHealth_RuntimeError(t *testing.T) {
	mockRuntime := new(MockRuntime)
	mockRuntime.On("Handle", mock.Anything, mock.Anything).
		Return(runtime.EvaluationResponse{}, errors.New("worker unavailable"))

	req := httptest.NewRequest(http.MethodGet, "/evaluate/health", nil)
	w := httptest.NewRecorder()

	newMuEdHandler(nil, mockRuntime, "").ServeHealth(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
	mockRuntime.AssertExpectations(t)
}

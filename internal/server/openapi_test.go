package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestLoadOpenAPISpec(t *testing.T) {
	spec, err := LoadOpenAPISpec()
	require.NoError(t, err)
	assert.NotNil(t, spec)
}

func TestOpenAPIMiddleware_Init(t *testing.T) {
	spec, err := LoadOpenAPISpec()
	require.NoError(t, err)

	middleware, err := OpenAPIMiddleware(spec, zap.NewNop())
	require.NoError(t, err)
	assert.NotNil(t, middleware)
}

func TestOpenAPIMiddleware_UnknownRoute_PassesThrough(t *testing.T) {
	middleware := mustMiddleware(t)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/not-a-mued-route", nil)
	w := httptest.NewRecorder()
	middleware(next).ServeHTTP(w, req)

	assert.True(t, called, "next handler should be called for unknown route")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOpenAPIMiddleware_ValidRequest_ReachesHandler(t *testing.T) {
	middleware := mustMiddleware(t)

	body := mustJSON(t, map[string]any{
		"submission": map[string]any{
			"type":    "TEXT",
			"content": map[string]any{"text": "hello"},
		},
	})

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`)) //nolint:errcheck
	})

	req := httptest.NewRequest(http.MethodPost, "/evaluate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	middleware(next).ServeHTTP(w, req)

	assert.True(t, called, "next handler should be called for valid request")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOpenAPIMiddleware_MissingRequiredField_Returns400(t *testing.T) {
	middleware := mustMiddleware(t)

	// POST /evaluate requires "submission"
	body := mustJSON(t, map[string]any{})

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler must not be called for invalid request")
	})

	req := httptest.NewRequest(http.MethodPost, "/evaluate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	middleware(next).ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestOpenAPIMiddleware_InvalidResponseBody_Returns500(t *testing.T) {
	middleware := mustMiddleware(t)

	body := mustJSON(t, map[string]any{
		"submission": map[string]any{
			"type":    "TEXT",
			"content": map[string]any{"text": "hello"},
		},
	})

	// handler returns an object, but spec requires an array for POST /evaluate 200
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"unexpected": "object"}`)) //nolint:errcheck
	})

	req := httptest.NewRequest(http.MethodPost, "/evaluate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	middleware(next).ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestOpenAPIMiddleware_ValidHealthRequest_ReachesHandler(t *testing.T) {
	middleware := mustMiddleware(t)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(mustJSON(t, map[string]any{ //nolint:errcheck
			"status": "OK",
			"capabilities": map[string]any{
				"supportsEvaluate":           true,
				"supportsPreSubmissionFeedback": false,
				"supportsFormativeFeedback":  true,
				"supportsSummativeFeedback":  true,
				"supportsDataPolicy":         "NOT_SUPPORTED",
			},
		}))
	})

	req := httptest.NewRequest(http.MethodGet, "/evaluate/health", nil)
	w := httptest.NewRecorder()
	middleware(next).ServeHTTP(w, req)

	assert.True(t, called, "next handler should be called for valid health request")
	assert.Equal(t, http.StatusOK, w.Code)
}

// mustMiddleware loads the real spec and returns the initialised middleware, failing the test on error.
func mustMiddleware(t *testing.T) func(http.Handler) http.Handler {
	t.Helper()
	spec, err := LoadOpenAPISpec()
	require.NoError(t, err)
	middleware, err := OpenAPIMiddleware(spec, zap.NewNop())
	require.NoError(t, err)
	return middleware
}

// mustJSON marshals v to JSON, failing the test on error.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
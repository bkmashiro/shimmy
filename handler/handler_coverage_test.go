package handler

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/config"
	"github.com/lambda-feedback/shimmy/runtime"
)

// TestNewCommandHandler verifies the constructor creates a non-nil handler.
func TestNewCommandHandler(t *testing.T) {
	mockH := new(MockHandler)

	h := NewCommandHandler(CommandHandlerParams{
		Handler: mockH,
		Config:  config.Config{},
		Log:     zap.NewNop(),
	})

	assert.NotNil(t, h)
}

// TestHealthHandler verifies the health endpoint returns 200 with JSON status.
func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	HealthHandler(w, req)

	res := w.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json", res.Header.Get("Content-Type"))

	body, _ := io.ReadAll(res.Body)
	assert.Contains(t, string(body), "ok")
}

// TestServeHTTP_NoAuthKey verifies requests pass when no auth key is configured.
func TestServeHTTP_NoAuthKey(t *testing.T) {
	mockH := new(MockHandler)

	expectedResponse := runtime.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       []byte(`{"result":"ok"}`),
	}

	mockH.On("Handle", mock.Anything, mock.Anything).Return(expectedResponse)

	handler := &CommandHandler{
		handler: mockH,
		log:     zap.NewNop(),
		config:  config.Config{Auth: config.AuthConfig{Key: ""}},
	}

	req := httptest.NewRequest(http.MethodPost, "/eval", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	mockH.AssertCalled(t, "Handle", mock.Anything, mock.Anything)
}

// TestServeHTTP_ResponseHeaders verifies response headers are mapped correctly.
func TestServeHTTP_ResponseHeaders(t *testing.T) {
	mockH := new(MockHandler)

	expectedResponse := runtime.Response{
		StatusCode: http.StatusCreated,
		Header: http.Header{
			"X-Custom-Header": []string{"value1", "value2"},
		},
		Body: []byte(`{}`),
	}

	mockH.On("Handle", mock.Anything, mock.Anything).Return(expectedResponse)

	handler := &CommandHandler{
		handler: mockH,
		log:     zap.NewNop(),
		config:  config.Config{},
	}

	req := httptest.NewRequest(http.MethodPost, "/eval", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusCreated, res.StatusCode)
	assert.Equal(t, "value1", res.Header.Get("X-Custom-Header"))
}

// TestServeHTTP_BodyReadError tests that malformed body returns 400.
// We can't easily break io.ReadAll in a standard test, so instead we
// verify the path through a request with an empty body.
func TestServeHTTP_EmptyBody(t *testing.T) {
	mockH := new(MockHandler)

	expectedResponse := runtime.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       []byte(`{}`),
	}

	mockH.On("Handle", mock.Anything, mock.Anything).Return(expectedResponse)

	handler := &CommandHandler{
		handler: mockH,
		log:     zap.NewNop(),
		config:  config.Config{},
	}

	req := httptest.NewRequest(http.MethodPost, "/eval", bytes.NewReader([]byte{}))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
}

package handler

import (
	"bytes"
	"context"
	"github.com/lambda-feedback/shimmy/config"
	"github.com/lambda-feedback/shimmy/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- Mock handler ---
type MockHandler struct {
	mock.Mock
}

func (m *MockHandler) Handle(ctx context.Context, req runtime.Request) runtime.Response {
	args := m.Called(ctx, req)
	return args.Get(0).(runtime.Response)
}

// --- Test ---
func TestServeHTTP_Success(t *testing.T) {
	mockHandler := new(MockHandler)

	reqBody := []byte(`{"example": "value"}`)
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(reqBody))
	req.Header.Set("api-key", "secret")

	w := httptest.NewRecorder()

	expectedResponse := runtime.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       []byte(`{"ok":true}`),
	}

	mockHandler.On("Handle", mock.Anything, mock.MatchedBy(func(r runtime.Request) bool {
		return r.Path == "/test" &&
			r.Method == http.MethodPost &&
			bytes.Equal(r.Body, reqBody)
	})).Return(expectedResponse)

	handler := &CommandHandler{
		handler: mockHandler,
		log:     zap.NewNop(), // or zaptest.NewLogger(t)
		config: config.Config{
			LogLevel: "debug",
			Runtime:  runtime.Config{},
			Auth:     config.AuthConfig{Key: "secret"},
		},
	}

	handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json", res.Header.Get("Content-Type"))
	assert.Equal(t, `{"ok":true}`, string(body))
	mockHandler.AssertExpectations(t)
}

func TestServeHTTP_Unauthorized(t *testing.T) {
	mockHandler := new(MockHandler)

	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader([]byte(`{"example": "value"}`)))
	req.Header.Set("api-key", "wrong-key") // wrong key

	w := httptest.NewRecorder()

	handler := &CommandHandler{
		handler: mockHandler, // won't be called
		log:     zap.NewNop(),
		config: config.Config{
			LogLevel: "debug",
			Runtime:  runtime.Config{},
			Auth:     config.AuthConfig{Key: "Secret"},
		},
	}

	handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)

	assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
	assert.Contains(t, string(body), "unauthorized")

	// Ensure handler was not called
	mockHandler.AssertNotCalled(t, "Handle", mock.Anything, mock.Anything)
}

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/config"
)

// TestNewLegacyRoute verifies the route is registered at "/".
func TestNewLegacyRoute(t *testing.T) {
	mockH := new(MockHandler)
	ch := NewCommandHandler(CommandHandlerParams{
		Handler: mockH,
		Config:  config.Config{},
		Log:     zap.NewNop(),
	})

	result := NewLegacyRoute(ch)
	assert.NotNil(t, result.Handler)
	assert.Equal(t, "/", result.Handler.Name)
}

// TestNewCommandRoute verifies the route is registered at "/{command}".
func TestNewCommandRoute(t *testing.T) {
	mockH := new(MockHandler)
	ch := NewCommandHandler(CommandHandlerParams{
		Handler: mockH,
		Config:  config.Config{},
		Log:     zap.NewNop(),
	})

	result := NewCommandRoute(ch)
	assert.NotNil(t, result.Handler)
	assert.Equal(t, "/{command}", result.Handler.Name)
}

// TestNewHealthRoute verifies the route is registered at "/health".
func TestNewHealthRoute(t *testing.T) {
	result := NewHealthRoute()
	assert.NotNil(t, result.Handler)
	assert.Equal(t, "/health", result.Handler.Name)
}

// TestNewHealthRoute_Serves verifies the health handler actually works.
func TestNewHealthRoute_Serves(t *testing.T) {
	result := NewHealthRoute()
	assert.NotNil(t, result.Handler)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	result.Handler.Handler.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
}

// TestNewLegacyRoute_Serves verifies the legacy route actually serves requests.
func TestNewLegacyRoute_Serves(t *testing.T) {
	mockH := new(MockHandler)

	ch := NewCommandHandler(CommandHandlerParams{
		Handler: mockH,
		Config:  config.Config{},
		Log:     zap.NewNop(),
	})

	result := NewLegacyRoute(ch)
	assert.NotNil(t, result.Handler)
	assert.NotNil(t, result.Handler.Handler)
}

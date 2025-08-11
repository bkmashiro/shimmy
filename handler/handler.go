package handler

import (
	"io"
	"net/http"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/config"
	"github.com/lambda-feedback/shimmy/runtime"
)

type CommandHandlerParams struct {
	fx.In

	Handler runtime.Handler
	Config  config.Config
	Log     *zap.Logger
}

func NewCommandHandler(params CommandHandlerParams) *CommandHandler {
	return &CommandHandler{
		handler: params.Handler,
		config:  params.Config,
		log:     params.Log,
	}
}

type CommandHandler struct {
	handler runtime.Handler
	config  config.Config
	log     *zap.Logger
}

func (h *CommandHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := h.log.With(
		zap.String("path", r.URL.Path),
		zap.String("method", r.Method),
	)

	// Check for authorization
	if h.config.Auth.Key != "" && r.Header.Get("api-key") != h.config.Auth.Key {
		log.Debug("unauthorized request")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Debug("failed to read body", zap.Error(err))
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	request := runtime.Request{
		Path:   r.URL.Path,
		Method: r.Method,
		Header: r.Header,
		Body:   body,
	}

	// Handle the request
	response := h.handler.Handle(r.Context(), request)

	// Map response headers
	for k, v := range response.Header {
		for _, vv := range v {
			w.Header().Add(k, vv)
		}
	}

	// Write response headers and status code
	w.WriteHeader(response.StatusCode)

	// Write response body
	if _, err := w.Write(response.Body); err != nil {
		log.Debug("failed to write response", zap.Error(err))
		http.Error(w, "failed to write response", http.StatusInternalServerError)
	}
}

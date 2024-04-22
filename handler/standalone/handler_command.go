package standalone

import (
	"io"
	"net/http"

	"github.com/lambda-feedback/shimmy/runtime"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type CommandHandlerParams struct {
	fx.In

	Handler runtime.Handler
	Logger  *zap.Logger
}

func NewCommandHandler(params CommandHandlerParams) HttpHandlerResult {
	handler := &CommandHandler{
		handler: params.Handler,
		log:     params.Logger,
	}

	return AsHttpHandler("/{command}", handler)
}

type CommandHandler struct {
	handler runtime.Handler
	log     *zap.Logger
}

func (h *CommandHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := h.log.With(
		zap.String("path", r.URL.Path),
		zap.String("method", r.Method),
	)

	// Read the body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Debug("failed to read body", zap.Error(err))
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	request := runtime.Request{
		Path:   r.URL.Path,
		Method: r.Method,
		Body:   body,
		Header: r.Header,
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

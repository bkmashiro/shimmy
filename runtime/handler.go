package runtime

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/lambda-feedback/shimmy/runtime/schema"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

var (
	errInvalidMethod    = errors.New("invalid method")
	errSchemaNotFound   = errors.New("schema not found")
	errCommandNotFound  = errors.New("command not found")
	errInvalidCommand   = errors.New("invalid command")
	errValidationFailed = errors.New("validation failed")
)

var wellKnownErrors = map[error]int{
	errInvalidMethod:    http.StatusMethodNotAllowed,
	errSchemaNotFound:   http.StatusInternalServerError,
	errCommandNotFound:  http.StatusNotFound,
	errInvalidCommand:   http.StatusBadRequest,
	errValidationFailed: http.StatusBadRequest,
}

// HandlerParams defines the dependencies for the runtime handler.
type HandlerParams struct {
	fx.In

	Runtime Runtime

	Log *zap.Logger
}

// Handler is the interface for handling runtime requests.
type Handler interface {
	Handle(ctx context.Context, request Request) Response
}

// RuntimeHandler is a runtime handler that uses a runtime to handle requests.
type RuntimeHandler struct {
	runtime Runtime

	schemas map[validationType]*schema.Schema

	log *zap.Logger
}

// NewRuntimeHandler creates a new runtime handler.
func NewRuntimeHandler(params HandlerParams) (Handler, error) {
	requestSchema, err := schema.NewRequestSchema()
	if err != nil {
		return nil, err
	}

	responseSchema, err := schema.NewResponseSchema()
	if err != nil {
		return nil, err
	}

	schemas := map[validationType]*schema.Schema{
		validationTypeRequest:  requestSchema,
		validationTypeResponse: responseSchema,
	}

	return &RuntimeHandler{
		runtime: params.Runtime,
		schemas: schemas,
		log:     params.Log,
	}, nil
}

// Handle handles a runtime request.
func (h *RuntimeHandler) Handle(ctx context.Context, req Request) Response {
	log := h.log.With(
		zap.String("path", req.Path),
		zap.String("method", req.Method),
	)

	if req.Method != http.MethodPost {
		log.Debug("invalid method")
		return newErrorResponse(errInvalidMethod)
	}

	commandStr, ok := h.getCommand(req)
	if !ok {
		log.Debug("missing command")
		return newErrorResponse(errCommandNotFound)
	}

	log = log.With(zap.String("command", commandStr))

	// Parse the raw command string into a Command type
	command, ok := ParseCommand(commandStr)
	if !ok {
		log.Debug("invalid command")
		return newErrorResponse(errInvalidCommand)
	}

	// Validate the request data against the request schema
	err := h.validate(validationTypeRequest, command, req.Body)
	if err != nil {
		return newErrorResponse(err)
	}

	// Create a new message with the parsed command and request data
	requestMsg := NewMessage(command, req.Body)

	// Let the runtime handle the message
	responseMsg, err := h.runtime.Handle(ctx, requestMsg)
	if err != nil {
		log.Debug("failed to handle message", zap.Error(err))
		return newErrorResponse(err)
	}

	// Validate the response data against the response schema
	err = h.validate(validationTypeResponse, command, responseMsg.Data)
	if err != nil {
		return newErrorResponse(err)
	}

	// Return the response data
	return newResponse(http.StatusOK, responseMsg.Data)
}

// getCommand tries to extract the command from the request.
func (s *RuntimeHandler) getCommand(req Request) (string, bool) {
	if commandStr := req.Header.Get("command"); commandStr != "" {
		return commandStr, true
	}

	pathElements := strings.Split(strings.TrimPrefix(req.Path, "/"), "/")
	if len(pathElements) == 1 {
		return pathElements[0], true
	}

	return "", false
}

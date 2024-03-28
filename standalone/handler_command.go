package standalone

import (
	"encoding/json"
	"net/http"

	"github.com/lambda-feedback/shimmy/runtime"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type CommandHandlerParams struct {
	fx.In

	Runtime runtime.Runtime
	Logger  *zap.Logger
}

func NewCommandHandler(params CommandHandlerParams) HttpHandlerResult {
	handler := &CommandHandler{
		runtime: params.Runtime,
		log:     params.Logger,
	}

	return AsHttpHandler("/{command}", handler)
}

type CommandHandler struct {
	runtime runtime.Runtime
	log     *zap.Logger
}

func (h *CommandHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.log.Debug("invalid http method", zap.String("method", r.Method))
		http.Error(w, "invalid http method", http.StatusMethodNotAllowed)
		return
	}

	commandStr := r.PathValue("command")
	if commandStr == "" {
		h.log.Debug("missing command")
		http.Error(w, "missing command", http.StatusNotFound)
	}

	command, ok := runtime.ParseCommand(commandStr)
	if !ok {
		h.log.Debug("invalid command", zap.String("command", commandStr))
		http.Error(w, "invalid command", http.StatusNotFound)
		return
	}

	var message runtime.Message
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
		h.log.Debug("failed to decode body", zap.Error(err))
		http.Error(w, "failed to decode body", http.StatusBadRequest)
		return
	}

	message.Command = command

	message, err := h.runtime.Handle(r.Context(), message)
	if err != nil {
		h.log.Error("failed to handle message", zap.Error(err))
		http.Error(w, "failed to handle message", http.StatusInternalServerError)
		return
	}

	if err := json.NewEncoder(w).Encode(message); err != nil {
		h.log.Error("failed to encode response", zap.Error(err))
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

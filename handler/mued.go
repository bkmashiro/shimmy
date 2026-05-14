package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/config"
	"github.com/lambda-feedback/shimmy/runtime"
)

type MuEdHandlerParams struct {
	fx.In

	Handler runtime.Handler
	Runtime runtime.Runtime
	Config  config.Config
	Log     *zap.Logger
}

type MuEdHandler struct {
	handler runtime.Handler
	runtime runtime.Runtime
	config  config.Config
	log     *zap.Logger
}

func NewMuEdHandler(params MuEdHandlerParams) *MuEdHandler {
	return &MuEdHandler{
		handler: params.Handler,
		runtime: params.Runtime,
		config:  params.Config,
		log:     params.Log,
	}
}

func (h *MuEdHandler) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if h.config.Auth.Key != "" && r.Header.Get("api-key") != h.config.Auth.Key {
		h.log.Debug("unauthorized request", zap.String("path", r.URL.Path))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// ServeEvaluate handles POST /evaluate.
func (h *MuEdHandler) ServeEvaluate(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var muEdReq runtime.MuEdEvaluateRequest
	if err := json.Unmarshal(body, &muEdReq); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	isPreview := muEdReq.PreSubmissionFeedback != nil && muEdReq.PreSubmissionFeedback.Enabled

	var legacyBody map[string]any
	if isPreview {
		legacyBody, err = runtime.MuEdBuildLegacyPreviewRequest(muEdReq)
	} else {
		legacyBody, err = runtime.MuEdBuildLegacyEvaluateRequest(muEdReq)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	legacyBodyBytes, err := json.Marshal(legacyBody)
	if err != nil {
		http.Error(w, "failed to build request", http.StatusInternalServerError)
		return
	}

	command := runtime.CommandEvaluate
	if isPreview {
		command = runtime.CommandPreview
	}

	header := http.Header{}
	header.Set("Command", string(command))

	req := runtime.Request{
		Path:   r.URL.Path,
		Method: http.MethodPost,
		Body:   legacyBodyBytes,
		Header: header,
	}

	resp := h.handler.Handle(r.Context(), req)

	if resp.StatusCode != http.StatusOK {
		for k, v := range resp.Header {
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(resp.Body) //nolint:errcheck
		return
	}

	var respBody map[string]any
	if err := json.Unmarshal(resp.Body, &respBody); err != nil {
		http.Error(w, "failed to parse response", http.StatusInternalServerError)
		return
	}

	result, ok := respBody["result"].(map[string]any)
	if !ok {
		http.Error(w, "invalid response from evaluation function", http.StatusInternalServerError)
		return
	}

	var feedback []map[string]any
	if isPreview {
		feedback = runtime.MuEdToPreviewFeedback(result)
	} else {
		feedback = runtime.MuEdToEvaluateFeedback(result)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(feedback) //nolint:errcheck
}

// ServeHealth handles GET /evaluate/health.
func (h *MuEdHandler) ServeHealth(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp, err := h.runtime.Handle(r.Context(), runtime.EvaluationRequest{
		Command: runtime.CommandHealth,
		Data:    map[string]any{},
	})
	if err != nil {
		http.Error(w, "health check failed", http.StatusInternalServerError)
		return
	}

	result, ok := resp["result"]
	if !ok {
		http.Error(w, "invalid health response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result) //nolint:errcheck
}

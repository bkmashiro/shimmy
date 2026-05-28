package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/lambda-feedback/shimmy/config"
	"github.com/lambda-feedback/shimmy/runtime"
)

const muEdVersionHeader = "X-Api-Version"

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

func writeJSONError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg}}) //nolint:errcheck
}

// checkMuEdVersion validates the X-Api-Version request header.
// Returns (resolvedVersion, true) on success, or writes a 406 and returns ("", false).
func (h *MuEdHandler) checkMuEdVersion(w http.ResponseWriter, r *http.Request) (string, bool) {
	requested := r.Header.Get(muEdVersionHeader)
	if requested != "" && !runtime.MuEdIsVersionSupported(requested) {
		body, _ := json.Marshal(map[string]any{
			"title": "API version not supported",
			"message": fmt.Sprintf(
				"The requested API version '%s' is not supported. Supported versions are: %v.",
				requested, runtime.SupportedMuEdVersions,
			),
			"code": "VERSION_NOT_SUPPORTED",
			"details": map[string]any{
				"requestedVersion":  requested,
				"supportedVersions": runtime.SupportedMuEdVersions,
			},
		})
		w.Header().Set(muEdVersionHeader, runtime.MuEdResolveVersion(requested))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotAcceptable)
		w.Write(body) //nolint:errcheck
		return "", false
	}
	return runtime.MuEdResolveVersion(requested), true
}

// writeMuEdError writes a structured muEd JSON error response with X-Api-Version header.
func (h *MuEdHandler) writeMuEdError(w http.ResponseWriter, version string, statusCode int, code, title, message string, details map[string]any) {
	body, _ := json.Marshal(map[string]any{
		"title":   title,
		"message": message,
		"code":    code,
		"details": details,
	})
	w.Header().Set(muEdVersionHeader, version)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	w.Write(body) //nolint:errcheck
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

	version, ok := h.checkMuEdVersion(w, r)
	if !ok {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeMuEdError(w, version, http.StatusBadRequest, "VALIDATION_ERROR", "Bad request", "failed to read body", nil)
		return
	}

	var muEdReq runtime.MuEdEvaluateRequest
	if err := json.Unmarshal(body, &muEdReq); err != nil {
		h.writeMuEdError(w, version, http.StatusBadRequest, "VALIDATION_ERROR", "Bad request", "invalid request body", nil)
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
		h.writeMuEdError(w, version, http.StatusBadRequest, "VALIDATION_ERROR", "Bad request", err.Error(), nil)
		return
	}

	legacyBodyBytes, err := json.Marshal(legacyBody)
	if err != nil {
		h.writeMuEdError(w, version, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", "failed to build request", nil)
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
		w.Header().Set(muEdVersionHeader, version)
		w.WriteHeader(resp.StatusCode)
		w.Write(resp.Body) //nolint:errcheck
		return
	}

	var respBody map[string]any
	if err := json.Unmarshal(resp.Body, &respBody); err != nil {
		h.writeMuEdError(w, version, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", "failed to parse response", nil)
		return
	}

	result, ok := respBody["result"].(map[string]any)
	if !ok {
		h.writeMuEdError(w, version, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", "invalid response from evaluation function", nil)
		return
	}

	var feedback []map[string]any
	if isPreview {
		feedback = runtime.MuEdToPreviewFeedback(result)
	} else {
		feedback = runtime.MuEdToEvaluateFeedback(result)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(muEdVersionHeader, version)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(feedback) //nolint:errcheck
}

// ServeHealth handles GET /evaluate/health.
func (h *MuEdHandler) ServeHealth(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}

	version, ok := h.checkMuEdVersion(w, r)
	if !ok {
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
		h.writeMuEdError(w, version, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", "health check failed", nil)
		return
	}

	legacyResult, ok := resp["result"].(map[string]any)
	if !ok {
		h.writeMuEdError(w, version, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", "invalid health response", nil)
		return
	}

	result := runtime.MuEdToHealthResponse(legacyResult)

	statusCode := http.StatusOK
	if s, ok := result["status"].(string); ok && s == "UNAVAILABLE" {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(muEdVersionHeader, version)
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(result) //nolint:errcheck
}

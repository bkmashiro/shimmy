package handler

import (
	"net/http"

	"github.com/lambda-feedback/shimmy/internal/server"
)

func NewLegacyRoute(handler *CommandHandler) server.HttpHandlerResult {
	return server.AsHttpHandler("/", handler)
}

func NewHealthRoute() server.HttpHandlerResult {
	return server.AsHttpHandler("/health", http.HandlerFunc(HealthHandler))
}

func NewMuEdEvaluateRoute(handler *MuEdHandler) server.HttpHandlerResult {
	return server.AsHttpHandler("/evaluate", http.HandlerFunc(handler.ServeEvaluate))
}

func NewMuEdEvaluateHealthRoute(handler *MuEdHandler) server.HttpHandlerResult {
	return server.AsHttpHandler("/evaluate/health", http.HandlerFunc(handler.ServeHealth))
}

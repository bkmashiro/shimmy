package handler

import (
	"net/http"

	"github.com/lambda-feedback/shimmy/internal/server"
)

func NewLegacyRoute(handler *CommandHandler) server.HttpHandlerResult {
	return server.AsHttpHandler("/", handler)
}

func NewCommandRoute(handler *CommandHandler) server.HttpHandlerResult {
	return server.AsHttpHandler("/{command}", handler)
}

func NewHealthRoute() server.HttpHandlerResult {
	return server.AsHttpHandler("/health", http.HandlerFunc(HealthHandler))
}

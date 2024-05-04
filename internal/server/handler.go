package server

import (
	"net/http"

	"go.uber.org/fx"
)

type HttpHandler struct {
	Name    string
	Handler http.Handler
}

type HttpHandlerResult struct {
	fx.Out

	Handler *HttpHandler `group:"handlers"`
}

func AsHttpHandler(
	name string,
	handler http.Handler,
) HttpHandlerResult {
	return HttpHandlerResult{
		Handler: &HttpHandler{
			Name:    name,
			Handler: handler,
		},
	}
}

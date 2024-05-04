package server

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"go.uber.org/fx"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type HttpServerParams struct {
	fx.In

	Context context.Context

	Config HttpConfig

	Handlers []*HttpHandler `group:"handlers"`
	Logger   *zap.Logger
}

type HttpServer struct {
	ctx    context.Context
	host   string
	port   int
	server *http.Server
	log    *zap.Logger
}

func NewHttpServer(params HttpServerParams) *HttpServer {
	mux := http.NewServeMux()

	for _, handler := range params.Handlers {
		mux.Handle(handler.Name, handler.Handler)
	}

	var handler http.Handler = mux
	if params.Config.H2c {
		handler = h2c.NewHandler(mux, &http2.Server{})
	}

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", params.Config.Host, params.Config.Port),
		Handler: handler,
	}

	return &HttpServer{
		ctx:    params.Context,
		host:   params.Config.Host,
		port:   params.Config.Port,
		server: server,
		log:    params.Logger,
	}
}

func NewLifecycleServer(params HttpServerParams, lc fx.Lifecycle) *HttpServer {
	server := NewHttpServer(params)
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go server.Serve(ctx)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return server.Shutdown(ctx)
		},
	})
	return server
}

func (s *HttpServer) Serve(context.Context) error {
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	cfg := net.ListenConfig{}

	listener, err := cfg.Listen(
		ctx,
		"tcp",
		fmt.Sprintf("%s:%d", s.host, s.port),
	)

	if err != nil {
		s.log.With(zap.Error(err)).Fatal("failed to listen")
		return err
	}

	s.log.With(zap.String("address", listener.Addr().String())).Info("listening")

	if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
		s.log.With(zap.Error(err)).Error("failed to serve")
		return err
	}

	return nil
}

func (s *HttpServer) Shutdown(ctx context.Context) error {
	if err := s.server.Shutdown(ctx); err != nil {
		s.log.With(zap.Error(err)).Error("failed to shutdown")
		return err
	}

	return nil
}

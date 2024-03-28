package standalone

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/lambda-feedback/shimmy/runtime"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type ServerHandler struct {
	Name    string
	Handler http.Handler
}

func NewServerHandler(name string, handler http.Handler) *ServerHandler {
	return &ServerHandler{
		Name:    name,
		Handler: handler,
	}
}

type HttpConfig struct {
	Host string
	Port int
	H2c  bool
}

type HttpServerParams struct {
	fx.In

	Context context.Context

	Config HttpConfig

	Handlers []*ServerHandler `group:"handlers"`
	Runtime  runtime.Runtime
	Logger   *zap.Logger
}

type HttpServer struct {
	ctx     context.Context
	host    string
	port    int
	server  *http.Server
	runtime runtime.Runtime
	log     *zap.Logger
}

func NewHttpServer(params HttpServerParams) *HttpServer {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", http.HandlerFunc(healthHandler))

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
		ctx:     params.Context,
		host:    params.Config.Host,
		port:    params.Config.Port,
		server:  server,
		runtime: params.Runtime,
		log:     params.Logger,
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

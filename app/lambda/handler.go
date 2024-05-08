package lambda

import (
	"context"
	"fmt"
	"net/http"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/lambda-feedback/shimmy/internal/server"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// LambdaHandlerParams represents the parameters required for
// the Lambda handler.
type LambdaHandlerParams struct {
	fx.In

	// Config is the configuration for the Lambda handler.
	Config Config

	// Handlers is a slice of HTTP handlers grouped together.
	Handlers []*server.HttpHandler `group:"handlers"`

	// Context is the context for the Lambda handler.
	Context context.Context

	// Logger is the logger for the Lambda handler.
	Logger *zap.Logger
}

type LambdaHandler struct {
	config Config
	ctx    context.Context
	cancel context.CancelFunc
	mux    *http.ServeMux
	log    *zap.Logger
}

// NewLambdaHandler creates a new instance of LambdaHandler
// with the given parameters.
func NewLambdaHandler(params LambdaHandlerParams) *LambdaHandler {
	ctx, cancel := context.WithCancel(params.Context)

	mux := http.NewServeMux()

	for _, handler := range params.Handlers {
		mux.Handle(handler.Name, handler.Handler)
	}

	return &LambdaHandler{
		config: params.Config,
		ctx:    ctx,
		cancel: cancel,
		mux:    mux,
		log:    params.Logger,
	}
}

// NewLifecycleHandler creates a new instance of LambdaHandler
// with the given parameters and attaches lifecycle hooks to
// start and stop the handler.
func NewLifecycleHandler(params LambdaHandlerParams, lc fx.Lifecycle) *LambdaHandler {
	handler := NewLambdaHandler(params)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			return handler.Start()
		},
		OnStop: func(context.Context) error {
			handler.Shutdown()
			return nil
		},
	})
	return handler
}

// Start starts the Lambda handler in a new goroutine. An error
// is returned if the handler fails to start.
func (s *LambdaHandler) Start() error {
	handler, err := s.getProxyFunction()
	if err != nil {
		return err
	}

	s.log.Debug("using lambda event proxy", zap.Stringer("proxy_source", s.config.ProxySource))

	go lambda.StartWithOptions(handler, lambda.WithContext(s.ctx))

	return nil
}

// Shutdown cancels the execution of the LambdaHandler.
func (s *LambdaHandler) Shutdown() {
	s.cancel()
}

// getProxyFunction returns the appropriate proxy function
// based on the configured ProxySource.
func (s *LambdaHandler) getProxyFunction() (any, error) {
	switch s.config.ProxySource {
	case ProxySourceApiGatewayV1:
		return httpadapter.New(s.mux).ProxyWithContext, nil
	case ProxySourceApiGatewayV2:
		return httpadapter.NewV2(s.mux).ProxyWithContext, nil
	case ProxySourceAlb:
		return httpadapter.NewALB(s.mux).ProxyWithContext, nil
	default:
		return nil, fmt.Errorf("invalid proxy source: %s", s.config.ProxySource)
	}
}

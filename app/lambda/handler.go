package lambda

import (
	"context"
	"net/http"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/lambda-feedback/shimmy/internal/server"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type LambdaHandlerParams struct {
	fx.In

	Context context.Context

	Handlers []*server.HttpHandler `group:"handlers"`

	Logger *zap.Logger
}

type LambdaHandler struct {
	ctx    context.Context
	cancel context.CancelFunc
	mux    *http.ServeMux
	log    *zap.Logger
}

func NewLambdaHandler(params LambdaHandlerParams) *LambdaHandler {
	ctx, cancel := context.WithCancel(params.Context)

	mux := http.NewServeMux()

	for _, handler := range params.Handlers {
		mux.Handle(handler.Name, handler.Handler)
	}

	return &LambdaHandler{
		ctx:    ctx,
		cancel: cancel,
		mux:    mux,
		log:    params.Logger,
	}
}

func NewLifecycleHandler(params LambdaHandlerParams, lc fx.Lifecycle) *LambdaHandler {
	handler := NewLambdaHandler(params)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go handler.Start()
			return nil
		},
		OnStop: func(context.Context) error {
			handler.Shutdown()
			return nil
		},
	})
	return handler
}

func (s *LambdaHandler) Start() {
	lambda.StartWithOptions(
		httpadapter.New(s.mux).ProxyWithContext,
		lambda.WithContext(s.ctx),
	)
}

func (s *LambdaHandler) Shutdown() {
	s.cancel()
}

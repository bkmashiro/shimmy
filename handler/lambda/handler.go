package lambda

import (
	"context"
	"net/http"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/lambda-feedback/shimmy/handler/common"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type LambdaHandlerParams struct {
	fx.In

	Context context.Context
	Handler *common.CommandHandler
	Logger  *zap.Logger
}

type LambdaHandler struct {
	ctx     context.Context
	cancel  context.CancelFunc
	handler *common.CommandHandler
	log     *zap.Logger
}

func NewLambdaHandler(params LambdaHandlerParams) *LambdaHandler {
	ctx, cancel := context.WithCancel(params.Context)

	return &LambdaHandler{
		ctx:     ctx,
		cancel:  cancel,
		handler: params.Handler,
		log:     params.Logger,
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

// func (s *LambdaHandler) Handle(
// 	ctx context.Context,
// 	evt events.APIGatewayProxyRequest,
// ) (events.APIGatewayProxyResponse, error) {
// 	var reqHeader http.Header
// 	for k, v := range evt.Headers {
// 		reqHeader.Add(k, v)
// 	}

// 	request := runtime.Request{
// 		Path:   evt.Path,
// 		Method: evt.HTTPMethod,
// 		Body:   []byte(evt.Body),
// 		Header: reqHeader,
// 	}

// 	response := s.handler.Handle(ctx, request)

// 	resHeader := make(map[string]string)
// 	for k, v := range response.Header {
// 		resHeader[k] = v[0]
// 	}

// 	return events.APIGatewayProxyResponse{
// 		StatusCode: response.StatusCode,
// 		Body:       string(response.Body),
// 		Headers:    resHeader,
// 	}, nil
// }

func (s *LambdaHandler) Start() {
	mux := http.NewServeMux()

	mux.HandleFunc("/", s.handler.ServeHTTP)

	lambda.StartWithOptions(
		httpadapter.New(mux).ProxyWithContext,
		lambda.WithContext(s.ctx),
	)
}

func (s *LambdaHandler) Shutdown() {
	s.cancel()
}

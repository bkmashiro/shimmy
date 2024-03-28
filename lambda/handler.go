package lambda

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/lambda-feedback/shimmy/runtime"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type LambdaHandlerParams struct {
	fx.In

	Context context.Context
	Runtime runtime.Runtime
	Logger  *zap.Logger
}

type LambdaHandler struct {
	ctx     context.Context
	cancel  context.CancelFunc
	runtime runtime.Runtime
	log     *zap.Logger
}

func NewLambdaHandler(params LambdaHandlerParams) *LambdaHandler {
	ctx, cancel := context.WithCancel(params.Context)

	return &LambdaHandler{
		ctx:     ctx,
		cancel:  cancel,
		runtime: params.Runtime,
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

func (s *LambdaHandler) Handle(
	ctx context.Context,
	evt events.APIGatewayProxyRequest,
) (events.APIGatewayProxyResponse, error) {
	if evt.HTTPMethod != http.MethodPost {
		s.log.Debug("invalid http method", zap.String("method", evt.HTTPMethod))
		return events.APIGatewayProxyResponse{StatusCode: http.StatusMethodNotAllowed}, nil
	}

	commandStr, ok := evt.PathParameters["command"]
	if !ok {
		s.log.Debug("missing command")
		return events.APIGatewayProxyResponse{StatusCode: http.StatusNotFound}, nil
	}

	command, ok := runtime.ParseCommand(commandStr)
	if !ok {
		s.log.Debug("invalid command", zap.String("command", commandStr))
		return events.APIGatewayProxyResponse{StatusCode: http.StatusNotFound}, nil
	}

	var message runtime.Message
	if err := json.Unmarshal([]byte(evt.Body), &message); err != nil {
		s.log.Debug("failed to unmarshal body", zap.Error(err))
		return events.APIGatewayProxyResponse{StatusCode: http.StatusBadRequest}, nil
	}

	message.Command = command

	message, err := s.runtime.Handle(ctx, message)
	if err != nil {
		s.log.Debug("failed to handle message", zap.Error(err))
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
		}, nil
	}

	responseData, err := json.Marshal(message)
	if err != nil {
		s.log.Debug("failed to marshal response", zap.Error(err))
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
		}, nil
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(responseData),
	}, nil
}

func (s *LambdaHandler) Start() {
	lambda.StartWithOptions(s.Handle, lambda.WithContext(s.ctx))
}

func (s *LambdaHandler) Shutdown() {
	s.cancel()
}

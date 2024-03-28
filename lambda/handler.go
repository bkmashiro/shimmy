package lambda

import (
	"context"
	"encoding/json"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/lambda-feedback/shimmy/runtime"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// type MyEvent struct {
// 	Name string `json:"name"`
// }

// func handleRequest() (*string, error) {
// 	if event == nil {
// 		return nil, fmt.Errorf("received nil event")
// 	}
// 	message := fmt.Sprintf("Hello %s!", event.Name)
// 	// TODO: deserialize lambda feedback event from the lambda event and send to the handler
// 	return &message, nil
// }

type LambdaHandlerParams struct {
	fx.In

	Runtime runtime.Runtime
	Logger  *zap.Logger
}

type LambdaHandler struct {
	runtime runtime.Runtime
	log     *zap.Logger
}

func NewLambdaHandler(params LambdaHandlerParams) *LambdaHandler {
	return &LambdaHandler{
		runtime: params.Runtime,
		log:     params.Logger,
	}
}

func (s *LambdaHandler) Handle(
	ctx context.Context,
	evt events.APIGatewayProxyRequest,
) (events.APIGatewayProxyResponse, error) {
	if evt.HTTPMethod != "POST" {
		s.log.Debug("invalid http method", zap.String("method", evt.HTTPMethod))
		return events.APIGatewayProxyResponse{StatusCode: 405}, nil
	}

	commandStr, ok := evt.PathParameters["command"]
	if !ok {
		s.log.Debug("missing command")
		return events.APIGatewayProxyResponse{StatusCode: 404}, nil
	}

	command, ok := runtime.ParseCommand(commandStr)
	if !ok {
		s.log.Debug("invalid command", zap.String("command", commandStr))
		return events.APIGatewayProxyResponse{StatusCode: 404}, nil
	}

	var message runtime.Message
	if err := json.Unmarshal([]byte(evt.Body), &message); err != nil {
		s.log.Debug("failed to unmarshal body", zap.Error(err))
		return events.APIGatewayProxyResponse{StatusCode: 400}, nil
	}

	message.Command = command

	message, err := s.runtime.Handle(
		ctx,
		message,
	)
	if err != nil {
		s.log.Debug("failed to handle message", zap.Error(err))
		return events.APIGatewayProxyResponse{StatusCode: 500}, nil
	}

	responseData, err := json.Marshal(message)
	if err != nil {
		s.log.Debug("failed to marshal response", zap.Error(err))
		return events.APIGatewayProxyResponse{StatusCode: 500}, nil
	}

	return events.APIGatewayProxyResponse{
		StatusCode: 200,
		Body:       string(responseData),
	}, nil
}

func (s *LambdaHandler) Start(ctx context.Context) {
	lambda.StartWithOptions(
		s.Handle,
		lambda.WithContext(ctx),
	)
}

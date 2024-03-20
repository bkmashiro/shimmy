package lambda

import (
	"context"
	"fmt"

	awslambda "github.com/aws/aws-lambda-go/lambda"
)

type MyEvent struct {
	Name string `json:"name"`
}

func handleRequest(ctx context.Context, event *MyEvent) (*string, error) {
	if event == nil {
		return nil, fmt.Errorf("received nil event")
	}
	message := fmt.Sprintf("Hello %s!", event.Name)
	// TODO: deserialize lambda feedback event from the lambda event and send to the handler
	return &message, nil
}

func Start() {
	awslambda.Start(handleRequest)
}

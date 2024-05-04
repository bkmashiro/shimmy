package cmd

import (
	"github.com/lambda-feedback/shimmy/app"
	"github.com/lambda-feedback/shimmy/app/lambda"
	"github.com/lambda-feedback/shimmy/util/logging"
	"github.com/urfave/cli/v2"
)

var (
	lambdaCmdDescription = `The lambda command starts the shim as an AWS Lambda runtime
interface client, which allows it to be directly invoked by
the AWS Lambda runtime without any additional dependencies.
This command is intended to be used as the entrypoint for a
dockerized evaluation function, written in any language.

The command will start the AWS runtime interface client and
blocks indefinitely, processing incoming AWS Lambda events.`
	lambdaCmd = &cli.Command{
		Name:        "lambda",
		Usage:       "Run the AWS Lambda handler",
		Description: lambdaCmdDescription,
		Action:      lambdaAction,
	}
)

func lambdaAction(ctx *cli.Context) error {
	log, err := logging.LoggerFromContext(ctx.Context)
	if err != nil {
		return err
	}

	app, err := app.New(ctx)
	if err != nil {
		return err
	}

	log.Info("starting AWS Lambda handler")

	return app.Run(ctx.Context, lambda.Module())
}

func init() {
	rootApp.Commands = append(rootApp.Commands, lambdaCmd)
}

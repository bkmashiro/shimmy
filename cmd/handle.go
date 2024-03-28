package cmd

import (
	"github.com/lambda-feedback/shimmy/app"
	"github.com/lambda-feedback/shimmy/lambda"
	"github.com/urfave/cli/v2"
)

var (
	handleCmdDescription = `The handle command starts the shim as an AWS Lambda runtime
interface client, which allows it to be directly invoked by
the AWS Lambda runtime without any additional dependencies.
This command is intended to be used as the entrypoint for a
dockerized evaluation function, written in any language.

The command will start the AWS runtime interface client and
blocks indefinitely, processing incoming AWS Lambda events.`
	handleCmd = &cli.Command{
		Name:        "handle",
		Usage:       "Run the AWS Lambda handler",
		Description: handleCmdDescription,
		Action:      handleAction,
	}
)

func handleAction(ctx *cli.Context) error {
	app, err := app.New(ctx)
	if err != nil {
		return err
	}

	return app.Run(ctx.Context, lambda.Module())
}

func init() {
	rootApp.Commands = append(rootApp.Commands, handleCmd)
}

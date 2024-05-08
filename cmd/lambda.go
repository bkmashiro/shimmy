package cmd

import (
	"github.com/lambda-feedback/shimmy/app"
	"github.com/lambda-feedback/shimmy/app/lambda"
	"github.com/lambda-feedback/shimmy/util/conf"
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
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "lambda-proxy-source",
				Usage:    "the source of the AWS Lambda event. Options: API_GW_V1, API_GW_V2, ALB.",
				Value:    "API_GW_V2",
				EnvVars:  []string{"LAMBDA_PROXY_SOURCE"},
				Category: "lambda",
			},
		},
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

	cfg, err := conf.Parse[lambda.Config](conf.ParseOptions{
		Log: log,
		Cli: ctx,
	})
	if err != nil {
		return err
	}

	log.Info("starting AWS Lambda handler")

	return app.Run(ctx.Context, lambda.Module(cfg))
}

func init() {
	rootApp.Commands = append(rootApp.Commands, lambdaCmd)
}

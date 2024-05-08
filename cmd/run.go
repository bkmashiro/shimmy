package cmd

import (
	"os"

	"github.com/lambda-feedback/shimmy/util/logging"
	"github.com/urfave/cli/v2"
)

var (
	runCmdDescription = `The run command detects the execution environment from the
environment variables and starts the shim. This allows the
shim to be executed on arbitrary platforms, without having
to define the server configuration at buildtime.

If the AWS_LAMBDA_RUNTIME_API environment variable is set,
shimmy will start the AWS Lambda runtime handler, matching
the behaviour of the handle command.

Otherwise, shimmy will start the standalone http server.
	`
	runCmd = &cli.Command{
		Name:        "run",
		Usage:       "Detect execution environment and start shim.",
		Description: runCmdDescription,
		Action:      runAction,
		Flags:       []cli.Flag{},
	}
)

func runAction(ctx *cli.Context) error {
	log, err := logging.LoggerFromContext(ctx.Context)
	if err != nil {
		return err
	}

	if isAWSLambda() {
		log.Info("detected AWS Lambda environment")
		return lambdaAction(ctx)
	}

	log.Info("detected standalone environment")
	return serveAction(ctx)
}

func isAWSLambda() bool {
	env, ok := os.LookupEnv("AWS_LAMBDA_RUNTIME_API")
	return ok && env != ""
}

func init() {
	runCmd.Flags = append(runCmd.Flags, serveCmd.Flags...)
	runCmd.Flags = append(runCmd.Flags, lambdaCmd.Flags...)

	rootApp.Commands = append(rootApp.Commands, runCmd)
}

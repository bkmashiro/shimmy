package cmd

import (
	"github.com/lambda-feedback/shimmy/app"
	"github.com/lambda-feedback/shimmy/app/standalone"
	"github.com/lambda-feedback/shimmy/util/conf"
	"github.com/lambda-feedback/shimmy/util/logging"
	"github.com/urfave/cli/v2"
)

var (
	serveCmdDescription = `The serve command starts a http server and waits for events
to handle. This allows the shim to be executed on arbitrary
platforms, in turn enabling platform-agnostic deployment of
language-agnostic evaluation functions.
	
The command will launch the http server and blocks indefin-
itely, processing incoming http requests.`
	serveCmd = &cli.Command{
		Name:        "serve",
		Usage:       "Start a http server and listen for events.",
		Description: serveCmdDescription,
		Action:      serveAction,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "host",
				Aliases:  []string{"H"},
				Usage:    "The host to listen on.",
				Value:    "localhost",
				EnvVars:  []string{"HTTP_HOST"},
				Category: "http",
			},
			&cli.IntFlag{
				Name:     "port",
				Aliases:  []string{"P"},
				Usage:    "The port to listen on.",
				Value:    8080,
				EnvVars:  []string{"HTTP_PORT"},
				Category: "http",
			},
			&cli.BoolFlag{
				Name:     "h2c",
				Usage:    "Enable HTTP/2 cleartext upgrade.",
				Value:    false,
				EnvVars:  []string{"HTTP_H2C"},
				Category: "http",
			},
		},
	}
)

func serveAction(ctx *cli.Context) error {
	log, err := logging.LoggerFromContext(ctx.Context)
	if err != nil {
		return err
	}

	app, err := app.New(ctx)
	if err != nil {
		return err
	}

	cfg, err := conf.Parse[standalone.Config](conf.ParseOptions{
		Log: log,
		Cli: ctx,
	})
	if err != nil {
		return err
	}

	log.Info("starting standalone http server")

	return app.Run(ctx.Context, standalone.Module(cfg))
}

func init() {
	rootApp.Commands = append(rootApp.Commands, serveCmd)
}

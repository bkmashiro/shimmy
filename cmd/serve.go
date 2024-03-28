package cmd

import (
	"github.com/lambda-feedback/shimmy/app"
	"github.com/lambda-feedback/shimmy/standalone"
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
				Category: "http",
				EnvVars:  []string{"HTTP_HOST"},
			},
			&cli.IntFlag{
				Name:     "port",
				Aliases:  []string{"P"},
				Usage:    "The port to listen on.",
				Value:    8080,
				Category: "http",
				EnvVars:  []string{"HTTP_PORT"},
			},
			&cli.BoolFlag{
				Name:     "h2c",
				Usage:    "Enable HTTP/2 cleartext upgrade.",
				Value:    false,
				Category: "http",
				EnvVars:  []string{"HTTP_H2C"},
			},
		},
	}
)

func serveAction(ctx *cli.Context) error {
	app, err := app.New(ctx)
	if err != nil {
		return err
	}

	httpConfig := standalone.HttpConfig{
		Host: ctx.String("host"),
		Port: ctx.Int("port"),
		H2c:  ctx.Bool("h2c"),
	}

	return app.Run(ctx.Context, standalone.Module(httpConfig))
}

func init() {
	rootApp.Commands = append(rootApp.Commands, serveCmd)
}

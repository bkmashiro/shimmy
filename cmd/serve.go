package cmd

import (
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
	}
)

func serveAction(ctx *cli.Context) error {
	// TODO: start http server, handle requests and pass to manager
	return nil
}

func init() {
	rootApp.Commands = append(rootApp.Commands, serveCmd)
}

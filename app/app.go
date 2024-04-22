package app

import (
	"github.com/lambda-feedback/shimmy/config"
	"github.com/lambda-feedback/shimmy/internal/shell"
	"github.com/lambda-feedback/shimmy/runtime"
	"github.com/lambda-feedback/shimmy/util/conf"
	"github.com/lambda-feedback/shimmy/util/logging"
	"github.com/urfave/cli/v2"
	"go.uber.org/fx"
)

func New(ctx *cli.Context) (*shell.Shell, error) {
	log, err := logging.LoggerFromContext(ctx.Context)
	if err != nil {
		return nil, err
	}

	config, err := conf.GetConfigFromContext[config.Config](ctx.Context)
	if err != nil {
		return nil, err
	}

	appModule := fx.Module(
		"app",

		// provide global config
		fx.Supply(config),

		// provide runtime module
		runtime.Module(config.Runtime),
	)

	return shell.New(log, appModule), nil
}

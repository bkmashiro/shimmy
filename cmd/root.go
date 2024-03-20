package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/lambda-feedback/shimmy/config"
	"github.com/lambda-feedback/shimmy/util/conf"
	"github.com/lambda-feedback/shimmy/util/logging"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
)

var (
	appName  = "shimmy"
	appUsage = `A shim for running arbitrary, language-agnostic evaluation
functions on arbitrary, serverless platforms.`
	rootApp = &cli.App{
		Name:            appName,
		Usage:           appUsage,
		HideHelpCommand: true,
		Args:            true,
		Flags: []cli.Flag{
			// general flags
			&cli.StringFlag{
				Name:    "log-level",
				Usage:   "set the log level. Options: debug, info, warn, error, panic, fatal.",
				EnvVars: []string{"LOG_LEVEL"},
			},
			&cli.StringFlag{
				Name:    "log-format",
				EnvVars: []string{"LOG_FORMAT"},
			},
			// shim flags
			&cli.StringFlag{
				Name:     "interface",
				Usage:    "the interface to use for communication with the worker process. Options: stdio, file.",
				Aliases:  []string{"i"},
				Value:    "stdio",
				Category: "function",
				EnvVars:  []string{"FUNCTION_INTERFACE"},
			},
			&cli.StringFlag{
				Name:     "command",
				Usage:    "the command to invoke in order to start the worker process.",
				Aliases:  []string{"c"},
				Category: "function",
				EnvVars:  []string{"FUNCTION_COMMAND"},
				Required: true,
			},
			&cli.StringSliceFlag{
				Name:     "arg",
				Usage:    "additional arguments to pass to the worker process.",
				Aliases:  []string{"a"},
				Category: "function",
				EnvVars:  []string{"FUNCTION_ARGS"},
			},
			// TODO: probable use `reusable` or `recycle`, depending on what the default case is
			&cli.BoolFlag{
				Name:     "disposable",
				Usage:    "stop the worker process after handling a single event.",
				Aliases:  []string{"d"},
				Category: "function",
				EnvVars:  []string{"FUNCTION_DISPOSABLE"},
			},
			&cli.StringFlag{
				Name:     "encoding",
				Usage:    "the encoding of the event data. Options: json.",
				Aliases:  []string{"e"},
				Value:    "json",
				Category: "function",
				EnvVars:  []string{"FUNCTION_ENCODING"},
			},
			&cli.IntFlag{
				Name:     "min-procs",
				Usage:    "",
				Value:    0,
				Category: "function",
				EnvVars:  []string{"FUNCTION_MIN_PROCS"},
			},
			&cli.IntFlag{
				Name:     "max-procs",
				Usage:    "",
				Aliases:  []string{"n"},
				Value:    0,
				Category: "function",
				EnvVars:  []string{"FUNCTION_MAX_PROCS"},
			},
		},
		Before: func(ctx *cli.Context) error {
			// create the logger
			log, err := createLogger(ctx)
			if err != nil {
				return err
			}

			// inject logger into cli context
			ctx.Context = logging.ContextWithLogger(ctx.Context, log)

			// parse config using env
			cfg, err := conf.Parse[config.Config](conf.ParseOptions{
				Defaults: config.DefaultConfig,
				Log:      log,
			})
			if err != nil {
				return err
			}

			// inject the config into the cli context
			ctx.Context = conf.ContextWithConfig(ctx.Context, cfg)

			return nil
		},
		After: func(ctx *cli.Context) error {
			log, err := logging.LoggerFromContext(ctx.Context)
			if err != nil {
				return err
			}

			log.Sync()

			return nil
		},
	}
)

func init() {
	cli.VersionFlag = &cli.BoolFlag{
		Name:               "version",
		Usage:              "print the version",
		DisableDefaultText: true,
	}
}

type ExecuteParams struct {
	Version  string
	Compiled time.Time
}

func Execute(params ExecuteParams) {
	rootApp.Version = params.Version
	rootApp.Compiled = params.Compiled

	run(context.Background(), os.Args)
}

func run(ctx context.Context, args []string) {
	err := rootApp.RunContext(ctx, args)

	// if app exited without error, return
	if err == nil {
		return
	}

	fmt.Printf("exit error: %s\n", err.Error())

	// if app exited with ExitError, exit with given exit code

	// otherwise, exit with exit code 1
	os.Exit(1)
}

func createLogger(ctx *cli.Context) (*zap.Logger, error) {
	level := getLogLevelFromCLI(ctx)
	format := getLogFormatFromCLI(ctx)

	var config zap.Config
	if format == "production" {
		config = zap.NewProductionConfig()
	} else {
		config = zap.NewDevelopmentConfig()
	}

	config.InitialFields = map[string]any{
		"app": "shimmy",
	}

	config.Level = level

	return config.Build()
}

func getLogFormatFromCLI(ctx *cli.Context) string {
	format := ctx.String("log-format")
	if format != "" {
		return format
	}

	return "production"
}

func getLogLevelFromCLI(ctx *cli.Context) zap.AtomicLevel {
	lvl := ctx.String("log-level")

	if atom, err := zap.ParseAtomicLevel(lvl); err == nil {
		return atom
	}

	return zap.NewAtomicLevelAt(zap.InfoLevel)
}

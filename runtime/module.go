package runtime

import "go.uber.org/fx"

func Module(config Config) fx.Option {
	return fx.Module(
		"runtime",

		// provide runtime config
		fx.Supply(config),

		// provide runtime
		fx.Provide(New),

		// provide runtime handler
		fx.Provide(NewRuntimeHandler),
	)
}

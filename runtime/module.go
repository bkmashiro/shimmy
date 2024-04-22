package runtime

import "go.uber.org/fx"

// Module provides a runtime module.
func Module(config Config) fx.Option {
	return fx.Module(
		"runtime",

		// provide runtime config
		fx.Supply(config),

		// provide runtime
		fx.Provide(NewRuntime),

		// provide runtime handler
		fx.Provide(NewRuntimeHandler),
	)
}

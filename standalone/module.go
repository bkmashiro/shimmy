package standalone

import "go.uber.org/fx"

func Module(config HttpConfig) fx.Option {
	return fx.Module(
		"lambda",
		// provide config
		fx.Supply(config),
		// provide server
		fx.Provide(NewLifecycleServer),
		// TODO: add handlers
		// invoke server
		fx.Invoke(func(*HttpServer) {}),
	)
}

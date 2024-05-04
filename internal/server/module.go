package server

import "go.uber.org/fx"

func Module(config HttpConfig) fx.Option {
	return fx.Module("server",
		// provide config
		fx.Supply(config),
		// provide server
		fx.Provide(NewLifecycleServer),
		// invoke server
		fx.Invoke(func(*HttpServer) {}),
	)
}

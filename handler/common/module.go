package common

import "go.uber.org/fx"

func Module() fx.Option {
	return fx.Module("common",
		fx.Provide(NewCommandHandler),
		fx.Provide(NewLegacyRoute),
		fx.Provide(NewCommandRoute),
		fx.Provide(NewHealthRoute),
	)
}

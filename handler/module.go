package handler

import "go.uber.org/fx"

func Module() fx.Option {
	return fx.Module("common",
		fx.Provide(NewCommandHandler),
		fx.Provide(NewMuEdHandler),
		fx.Provide(NewLegacyRoute),
		fx.Provide(NewHealthRoute),
		fx.Provide(NewMuEdEvaluateRoute),
		fx.Provide(NewMuEdEvaluateHealthRoute),
	)
}

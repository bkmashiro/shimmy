package lambda

import "go.uber.org/fx"

func Module() fx.Option {
	return fx.Module(
		"lambda",
		// provide handler
		fx.Provide(NewLambdaHandler),
		// invoke handler
		fx.Invoke(func(*LambdaHandler) {}),
	)
}

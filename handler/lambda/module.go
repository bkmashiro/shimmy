package lambda

import (
	"github.com/lambda-feedback/shimmy/handler/common"
	"github.com/lambda-feedback/shimmy/util/logging"
	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Module(
		"lambda",
		// rename logger for module
		logging.DecorateLogger("lambda"),
		// provide command handler
		fx.Provide(common.NewCommandHandler),
		// provide handler
		fx.Provide(NewLifecycleHandler),
		// invoke handler
		fx.Invoke(func(*LambdaHandler) {}),
	)
}

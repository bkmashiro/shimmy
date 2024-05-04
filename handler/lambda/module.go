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
		// provide handlers
		common.Module(),
		// provide server
		fx.Provide(NewLifecycleHandler),
		// invoke server
		fx.Invoke(func(*LambdaHandler) {}),
	)
}

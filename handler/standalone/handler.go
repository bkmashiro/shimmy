package standalone

import "github.com/lambda-feedback/shimmy/handler/common"

func NewLegacyRoute(handler *common.CommandHandler) HttpHandlerResult {
	return AsHttpHandler("/", handler)
}

func NewCommandRoute(handler *common.CommandHandler) HttpHandlerResult {
	return AsHttpHandler("/{command}", handler)
}

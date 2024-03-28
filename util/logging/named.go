package logging

import (
	"go.uber.org/fx"
	"go.uber.org/zap"
)

func NamedLogger(name string) func(log *zap.Logger) *zap.Logger {
	return func(log *zap.Logger) *zap.Logger {
		return log.Named(name)
	}
}

func DecorateLogger(name string) fx.Option {
	return fx.Decorate(NamedLogger(name))
}

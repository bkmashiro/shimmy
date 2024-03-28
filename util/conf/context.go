package conf

import (
	"context"
	"errors"
)

type contextKey int

var configKey = contextKey(1)

func GetConfigFromContext[C any](ctx context.Context) (C, error) {
	var c C

	configValue := ctx.Value(configKey)

	if configValue == nil {
		return c, errors.New("config not found in context")
	}

	if config, ok := configValue.(C); ok {
		return config, nil
	}

	return c, errors.New("invalid config in context")
}

func ContextWithConfig[C any](ctx context.Context, config C) context.Context {
	return context.WithValue(ctx, configKey, config)
}

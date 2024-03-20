package logging

import (
	"context"
	"errors"

	"go.uber.org/zap"
)

type contextKey int

var loggerKey = contextKey(0)

var ErrNoLoggerInContext = errors.New("no logger in context")

func ContextWithLogger(ctx context.Context, logger *zap.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

func LoggerFromContext(ctx context.Context) (*zap.Logger, error) {
	if logger, ok := ctx.Value(loggerKey).(*zap.Logger); ok {
		return logger, nil
	}

	return nil, ErrNoLoggerInContext
}

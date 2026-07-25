package wasm

import "go.uber.org/zap"

func logSnapshotSelection(log *zap.Logger, requested, selected, fallbackReason string) {
	if log == nil {
		return
	}
	if requested == "" {
		requested = "memcpy"
	}
	fields := []zap.Field{
		zap.String("requested", requested),
		zap.String("selected", selected),
		zap.String("fallback_reason", fallbackReason),
	}
	if fallbackReason != "" {
		log.Warn("snapshot strategy selected with fallback", fields...)
		return
	}
	log.Info("snapshot strategy selected", fields...)
}

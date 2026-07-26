package wasm

import (
	"context"

	"go.uber.org/zap"
)

// poolItem is the interface satisfied by any item that can be shut down when
// draining a pool (for example wasmSupervisor or ResidentPythonRunner).
type poolItem interface {
	Shutdown(ctx context.Context) error
}

// drainPool receives up to cap(pool) items from the channel and calls
// Shutdown on each. If the context is cancelled before all items are drained,
// it logs a warning and returns early, avoiding the deadlock that occurs when
// an unhealthy item was discarded and its replacement goroutine hasn't
// finished yet.
func drainPool[T poolItem](ctx context.Context, pool chan T, log *zap.Logger) error {
	if pool == nil {
		return nil
	}

	var firstErr error
	for i := 0; i < cap(pool); i++ {
		select {
		case item := <-pool:
			if err := item.Shutdown(ctx); err != nil {
				log.Error("error shutting down pool item", zap.Error(err))
				if firstErr == nil {
					firstErr = err
				}
			}
		case <-ctx.Done():
			log.Warn("drainPool: context cancelled, some items may not be shut down",
				zap.Int("remaining", cap(pool)-i))
			return ctx.Err()
		}
	}
	return firstErr
}

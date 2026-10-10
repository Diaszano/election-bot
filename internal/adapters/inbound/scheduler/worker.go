// Package scheduler runs cancelable, non-overlapping application ticks.
package scheduler

import (
	"context"
	"log/slog"
	"time"
)

func Run(ctx context.Context, interval time.Duration, operation string, logger *slog.Logger, tick func(context.Context) error) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if e := tick(ctx); e != nil && ctx.Err() == nil {
				logger.WarnContext(ctx, "worker operation failed", "operation", operation, "error", e)
			}
			timer.Reset(interval)
		}
	}
}

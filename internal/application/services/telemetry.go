package services

import (
	"context"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"log/slog"
	"time"
)

func Operation(ctx context.Context, name string) (context.Context, func(error)) {
	ctx, span := otel.Tracer("election-bot").Start(ctx, name)
	return ctx, func(e error) {
		if e != nil {
			span.RecordError(e)
			span.SetStatus(codes.Error, "operation failed")
		}
		span.End()
	}
}
func Counter(ctx context.Context, name string) {
	c, e := otel.Meter("election-bot").Int64Counter(name)
	if e != nil {
		slog.Warn("metric instrument unavailable", "instrument", name)
		return
	}
	c.Add(ctx, 1)
}
func Duration(ctx context.Context, name string, start time.Time) {
	h, e := otel.Meter("election-bot").Float64Histogram(name, metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30))
	if e != nil {
		slog.Warn("metric instrument unavailable", "instrument", name)
		return
	}
	h.Record(ctx, time.Since(start).Seconds())
}

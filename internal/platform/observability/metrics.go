package observability

import (
	"context"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"time"
)

type Values struct {
	LastSuccess, Published           time.Time
	Progress                         float64
	ValidVotes, Subscribers, Pending int64
	OldestPending                    time.Time
	Counting                         bool
}

func RegisterState(read func(context.Context) (Values, error)) error {
	m := otel.Meter("election-bot")
	names := []string{"election_last_success_timestamp", "election_publication_timestamp", "election_counted_sections_ratio", "election_valid_votes", "telegram_subscribers", "telegram_outbox_pending", "telegram_outbox_oldest_age", "election_counting_active"}
	gauges := make([]metric.Float64ObservableGauge, len(names))
	for i, n := range names {
		opts := []metric.Float64ObservableGaugeOption{}
		if i == 0 || i == 1 || i == 6 {
			opts = append(opts, metric.WithUnit("s"))
		}
		g, e := m.Float64ObservableGauge(n, opts...)
		if e != nil {
			return e
		}
		gauges[i] = g
	}
	instruments := make([]metric.Observable, len(gauges))
	for i, g := range gauges {
		instruments[i] = g
	}
	_, e := m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		v, e := read(ctx)
		if e != nil {
			return e
		}
		values := []float64{0, 0, v.Progress, float64(v.ValidVotes), float64(v.Subscribers), float64(v.Pending), 0, 0}
		if !v.LastSuccess.IsZero() {
			values[0] = float64(v.LastSuccess.Unix())
		}
		if !v.Published.IsZero() {
			values[1] = float64(v.Published.Unix())
		}
		if !v.OldestPending.IsZero() {
			values[6] = max(time.Since(v.OldestPending).Seconds(), 0)
		}
		if v.Counting {
			values[7] = 1
		}
		for i, g := range gauges {
			o.ObserveFloat64(g, values[i])
		}
		return nil
	}, instruments...)
	return e
}

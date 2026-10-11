package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// WhatsAppConnectionStateValue maps persisted, bounded state to a metric value:
// 0 unknown, 1 connected, 2 connecting, 3 disconnected, 4 restricted, 5 outdated, 6 auth required.
func WhatsAppConnectionStateValue(authenticationRequired bool, reason string) int64 {
	if authenticationRequired {
		return 6
	}
	switch reason {
	case "connected":
		return 1
	case "connecting":
		return 2
	case "disconnected", "transient":
		return 3
	case "restricted":
		return 4
	case "client_outdated":
		return 5
	case "not_paired", "account_unbound", "account_changed", "operator_logout", "authentication_required":
		return 6
	default:
		return 0
	}
}

func registerWhatsAppMetrics(m metric.Meter, read func(context.Context) (Values, error)) error {
	state, err := m.Float64ObservableGauge("whatsapp_connection_state")
	if err != nil {
		return err
	}
	subscribers, err := m.Int64ObservableGauge("whatsapp_subscribers")
	if err != nil {
		return err
	}
	pending, err := m.Int64ObservableGauge("whatsapp_pending_outbox")
	if err != nil {
		return err
	}
	failed, err := m.Int64ObservableGauge("whatsapp_failed_outbox")
	if err != nil {
		return err
	}
	lastSuccess, err := m.Float64ObservableGauge("whatsapp_last_success_timestamp", metric.WithUnit("s"))
	if err != nil {
		return err
	}
	_, err = m.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		values, err := read(ctx)
		if err != nil {
			return err
		}
		observer.ObserveFloat64(state, float64(values.WhatsAppConnectionState))
		observer.ObserveInt64(subscribers, values.WhatsAppSubscribers)
		observer.ObserveInt64(pending, values.WhatsAppPending)
		observer.ObserveInt64(failed, values.WhatsAppFailed)
		if !values.WhatsAppLastSuccess.IsZero() {
			observer.ObserveFloat64(lastSuccess, float64(values.WhatsAppLastSuccess.Unix()))
		}
		return nil
	}, state, subscribers, pending, failed, lastSuccess)
	return err
}

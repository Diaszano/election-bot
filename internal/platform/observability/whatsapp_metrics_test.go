package observability

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func collectWhatsAppMetrics(t *testing.T, enabled bool, values Values) map[string]metricdata.Aggregation {
	t.Helper()
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	if err := registerState(provider.Meter("election-bot"), func(context.Context) (Values, error) { return values, nil }, enabled); err != nil {
		t.Fatal(err)
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	got := make(map[string]metricdata.Aggregation)
	for _, scope := range data.ScopeMetrics {
		for _, sample := range scope.Metrics {
			got[sample.Name] = sample.Data
		}
	}
	return got
}

func TestWhatsAppTelemetryPrivacy(t *testing.T) {
	metrics := collectWhatsAppMetrics(t, true, Values{WhatsAppConnectionState: WhatsAppConnectionStateValue(false, "connected"), WhatsAppSubscribers: 1, WhatsAppPending: 2})
	b, err := json.Marshal(metrics)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"5511999999999", "123456@lid", "private message", "qr-payload", "session-key"} {
		if strings.Contains(string(b), private) {
			t.Fatalf("private WhatsApp value exported: %q", private)
		}
	}
	for name, aggregation := range metrics {
		if gauge, ok := aggregation.(metricdata.Gauge[float64]); ok {
			for _, point := range gauge.DataPoints {
				if len(point.Attributes.ToSlice()) != 0 {
					t.Fatalf("WhatsApp metric %s has attributes", name)
				}
			}
		}
	}
}

func TestWhatsAppMetricSemantics(t *testing.T) {
	last := time.Date(2026, 10, 25, 22, 0, 0, 0, time.UTC)
	metrics := collectWhatsAppMetrics(t, true, Values{WhatsAppConnectionState: WhatsAppConnectionStateValue(false, "connected"), WhatsAppSubscribers: 4, WhatsAppPending: 3, WhatsAppFailed: 2, WhatsAppLastSuccess: last})
	for _, name := range []string{"whatsapp_connection_state", "whatsapp_subscribers", "whatsapp_pending_outbox", "whatsapp_failed_outbox", "whatsapp_last_success_timestamp"} {
		if _, ok := metrics[name]; !ok {
			t.Fatalf("metric %s was not registered", name)
		}
	}
	if got := WhatsAppConnectionStateValue(true, "connected"); got != 6 {
		t.Fatalf("authentication-required state=%d", got)
	}
	if got := WhatsAppConnectionStateValue(false, "connected"); got != 1 {
		t.Fatalf("connected state=%d", got)
	}
}

func TestWhatsAppTelemetryDisabled(t *testing.T) {
	metrics := collectWhatsAppMetrics(t, false, Values{})
	for name := range metrics {
		if strings.HasPrefix(name, "whatsapp_") {
			t.Fatalf("disabled WhatsApp metric registered: %s", name)
		}
	}
}

func TestDashboardWhatsAppQueries(t *testing.T) {
	b, err := os.ReadFile("../../../deployments/grafana/dashboards/overview.json")
	if err != nil {
		t.Fatal(err)
	}
	var dashboard struct {
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err = json.Unmarshal(b, &dashboard); err != nil {
		t.Fatal(err)
	}
	joined, titles := "", ""
	for _, panel := range dashboard.Panels {
		titles += panel.Title + "\n"
		for _, target := range panel.Targets {
			joined += target.Expr + "\n"
		}
	}
	for _, title := range []string{"WhatsApp connection", "WhatsApp successful sends", "WhatsApp delivery failures", "WhatsApp reconnects", "WhatsApp subscribers", "WhatsApp pending notifications", "WhatsApp send latency", "WhatsApp last successful delivery"} {
		if !strings.Contains(titles, title) {
			t.Errorf("dashboard is missing panel %q", title)
		}
	}
	for _, title := range []string{"Telegram messages sent", "Telegram message edits", "Notification failures"} {
		if !strings.Contains(titles, title) {
			t.Errorf("existing Telegram panel was removed: %q", title)
		}
	}
	for _, metric := range []string{"whatsapp_connection_state", "whatsapp_messages_sent_total", "whatsapp_delivery_failures_total", "whatsapp_reconnects_total", "whatsapp_subscribers", "whatsapp_pending_outbox", "whatsapp_message_send_duration_seconds_bucket", "whatsapp_last_success_timestamp_seconds"} {
		if !strings.Contains(joined, metric) {
			t.Errorf("dashboard is missing query for %q", metric)
		}
	}
	telemetry, err := os.ReadFile("../../../deployments/grafana/dashboards/telemetry.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(telemetry), "whatsapp.*") || !strings.Contains(string(telemetry), "whatsapp_delivery_failures_total") || !strings.Contains(string(telemetry), "whatsapp_message_send_duration_seconds_bucket") {
		t.Fatal("Telemetry dashboard is missing WhatsApp trace, error or latency exploration")
	}
}

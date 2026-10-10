# Observability

The official OTel SDKs export traces, metrics and logs over OTLP gRPC. JSON slog also writes stdout; otelslog exports log records with native context correlation and explicit `trace_id`/`span_id` attributes. DEBUG/INFO/WARN/ERROR are supported. Meaningful accepted changes emit INFO; unchanged results do not. Export errors use the JSON handler directly to avoid recursively exporting an exporter-failure log.

Resource attributes: `service.name=election-bot`, `service.namespace=elections`, `service.version=dev`, `deployment.environment=<APP_ENV>`. TSE HTTP contexts propagate W3C TraceContext. Telegram uses sanitized logical spans (`telegram.send_message`, `telegram.edit_message`, command API calls), avoiding raw token-bearing HTTP URLs. No chat IDs, message IDs, source candidate names, private contents or credentials are signal labels/attributes.

## Traces and sampling

Application spans cover actual polling, persistence, publishing and subscription changes. Idle polling/publisher ticks create no spans. Errors are recorded with sanitized details. Configure `parentbased_traceidratio` and ratio in [0,1]; default 1.0 is suitable for this single-election workload. Parent relationships cross use cases and outbound calls. Trace/log batches and metric export happen in background; temporary exporter failures do not stop monitoring. Shutdown attempts to flush within a deadline, with incomplete export logged as a telemetry warning.

## Actual Prometheus names

These names/resource labels were checked against the running Collector exporter, not just SDK definitions. Counters initialize at zero; Telegram latency is absent until an API request occurs.

| Name | Type / meaning |
|---|---|
| `election_poll_total` | Counter of actual fetch cycles, not idle scheduler ticks |
| `election_poll_errors_total` | Counter of failed synchronization cycles |
| `election_poll_duration_seconds` | Histogram of poll duration |
| `election_last_success_timestamp_seconds` | Gauge: Unix epoch of successful synchronization |
| `election_publication_timestamp_seconds` | Gauge: source publication epoch |
| `election_snapshot_changes_total` | Counter of accepted meaningful changes |
| `election_counted_sections_ratio` | Gauge [0,1], official section progress, not candidate vote share |
| `election_valid_votes` | Gauge of current valid votes; corrections can decrease it |
| `election_counting_active` | Gauge 1 while counting, 0 otherwise |
| `election_poll_consecutive_failures` | Gauge for consecutive failure alert gating |
| `telegram_messages_sent_total` | Counter of successful actual Telegram sends |
| `telegram_messages_edited_total` | Counter of successful actual Telegram edits |
| `telegram_notifications_failed_total` | Counter of failed delivery attempts |
| `telegram_api_requests_total` | Counter of Telegram API attempts |
| `telegram_api_request_duration_seconds` | Histogram of API duration |
| `telegram_subscribers` | Gauge of active subscriptions |
| `telegram_outbox_pending` | Gauge of pending work |
| `telegram_outbox_oldest_age_seconds` | Gauge based on oldest pending due time, excluding future waits |

Histogram boundaries in seconds: .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30. Prometheus adds `_bucket`, `_sum`, `_count`. Labels include `service_name`, `service_namespace`, `service_version`, `deployment_environment`; scrape `job` is `election-bot`, while converted resource job may appear as `exported_job`. Never select subscriptions by private IDs.

## Backends and dashboards

Collector: OTLP 4317/4318 → memory limiter/batch → Tempo OTLP, Prometheus exporter 8889 and Loki native `/otlp` via `otlphttp`. Collector self-metrics are scraped on 8888. No deprecated Loki exporter is used. Loki uses v13 TSDB and structured metadata; resource `service.name` becomes label `service_name`.

Tempo 3.1 uses its documented single-binary configuration without an `ingester` section or Kafka. Grafana sources have UIDs `prometheus`, `tempo`, `loki`. **Election Bot — Overview** covers poll health, freshness, section progress, Telegram delivery/subscribers/latency and HTTP readiness. **Election Bot — Telemetry** provides logs, TraceQL search, operation errors, slow requests and Collector health. TraceQL search uses actual Tempo traces, not nonexistent Prometheus span metrics.

Select a log's TraceID link to open Tempo; Tempo trace-to-logs maps the service resource and filters on trace ID. Loki stores trace IDs as structured metadata. Grafana expressions containing its variables use normal `${...}` syntax in mounted files; Compose does not interpolate their file contents.

## Validation

```sh
OTEL_ENABLED=true OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4317 make simulate
export GRAFANA_ADMIN_PASSWORD
sh deployments/docker/verify-telemetry.sh
```

Local tests run a real gRPC OTLP receiver and assert receipt of all signals, resource values, correlation and absence of sentinel secrets/private IDs/content. Integration tests keep business readiness operational with an unreachable exporter. Live verification queries Prometheus and Loki, retrieves the correlated trace in Tempo and checks Grafana provisioning. See [validation evidence](validation.md).

## Alerts and runbooks

Prometheus evaluates rules every five seconds. Rules are provisioned; alert notification routing is not configured because no operator destination was supplied. Configure Alertmanager routing before relying on paging.

- **ElectionPollingFailure:** consecutive failures during active counting for five minutes. Check connectivity, TSE cooldown and documented URL. Never repeatedly probe a 404.
- **ElectionDataStale:** counting active with synchronization older than two minutes or publication older than five minutes, sustained one minute. Retain last valid results and distinguish synchronization from source publication lag.
- **TelegramDeliveryFailures:** more than five failed attempts over five minutes with pending work, sustained one minute. Check Retry-After, outbox due/attempts/error classification and blocked-chat state.
- **ElectionBotUnavailable:** failed/absent readiness probe for one minute. Check logs, storage permissions, disk space and migration state; do not blame unpublished election data for local readiness.
- **TelemetryExportFailures:** sustained Collector send failures for five minutes. Check Tempo/Loki/Collector queues and resource capacity. Confirm election workers remain healthy.

Freshness/poll-failure alerts are gated by active counting, so waiting and finalized states do not generate them. Availability/export alerts remain enabled. `promtool test rules` covers counting and waiting-state behavior. The offline simulator has synthetic timestamps and is not a real-election freshness benchmark.

## Troubleshooting

No metrics: query Collector :8889, then Prometheus targets. No logs: inspect Collector OTLP HTTP errors and Loki structured metadata/schema. No traces: inspect Tempo readiness and OTLP receiver binding; use a fresh sampled simulation trace. No links: verify source UIDs and `trace_id` metadata. Backend restart or sampled-out traces can leave a valid log without a retained trace. Flush is best effort on abrupt process death; business state is independently durable.

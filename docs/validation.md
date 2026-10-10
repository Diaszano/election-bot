# Verification ledger

Date: 2026-10-10. Implementation branch: `feat/election-bot`. Architecture/spec/plan were approved before product implementation. Native execution used incremental commits and test-first package/regression checks.

## Executed evidence

| Check | Result |
|---|---|
| Configuration, domain, formatting, SQLite, TSE, Telegram, application tests | PASS in `go test ./...` |
| Deterministic offline recovery scenario | PASS with `make simulate` |
| Executable SIGTERM with unreachable OTLP backend | PASS; process ready and exits within ten seconds |
| OTLP gRPC local receiver | PASS: actual logs, metrics, traces, resources/correlation and privacy sentinels |
| `go build ./cmd/election-bot` / `make build` | PASS |
| `go mod verify` | PASS |
| `go vet ./...` | PASS |
| golangci-lint 2.13.2 | PASS: zero issues |
| Docker multistage non-root image | PASS: image built and application container healthy |
| Application-only and merged Compose configuration | PASS: `docker compose ... config -q` |
| Collector configuration | PASS: running 0.162.0 `validate --config` |
| Prometheus configuration and alert tests | PASS: running 3.15.0 promtool check/test |
| Prometheus application metric query | PASS: `election_poll_total{service_name="election-bot"}` returned series |
| Loki native OTLP ingestion | PASS: application log with trace ID returned |
| Tempo correlated trace retrieval | PASS: trace `a3c4fa58a1cf875d53829867a88a0e93` retrieved |
| Grafana provisioning and readiness probe | PASS: verifier checked source/dashboard UIDs and probe success |

`go test -race ./...` passed across all packages, including the executable shutdown test. Independent-review results are appended after execution. Backend versions: Collector 0.162.0, Prometheus 3.15.0, Grafana 13.2.3, Tempo 3.1.0, Loki 3.7.8, probe exporter 0.29.0. Docker daemon access was initially denied but became available after permissions changed; Docker checks were subsequently executed.

## Integration boundary

No live Telegram account/token was supplied; actual public Telegram delivery was not performed. API behavior is tested through real HTTP fake servers. No official presidential runoff results were fabricated or consumed. Official first-round simulation artifacts were captured from TSE with hashes; EA20 PDF retrieval returned HTTP 403. Production mode remains fail-closed pending verification of the required runoff schema/path/status semantics. These are explicit limitations, not passing official-production acceptance checks.

## Decisions and known limits

- Reject changed equal-timestamp generations as ambiguous: IDs are opaque. A legitimate tied correction may wait for a later timestamp.
- Deliver retries over durable publisher ticks rather than sleeping inside a three-attempt dispatch: keeps other subscribers moving, with cadence-dependent retry latency.
- First-round sample valid-vote percentages are derived only for valid destinations; invalid destinations show unavailable valid share. First-round statuses never confirm a runoff winner.
- Tempo 3.1 uses the documented single-binary configuration; obsolete ingester fields were removed after a reproduced startup failure. No Kafka or extra application service was added.
- Single replica and fixed runoff identity scope the database. Main messages coalesce revisions; milestones retain durable history. At-least-once Telegram delivery can duplicate after ambiguous remote success.
- Prometheus rules are loaded and tested; operator paging is not configured without an Alertmanager destination.

See [fixture provenance](../test/fixtures/tse/README.md), [TSE contract](tse-contract.md), [architecture](architecture.md) and [observability](observability.md).

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
| Container SIGTERM shutdown | PASS: rebuilt application stopped with exit code 0; test stack removed with volumes preserved |
| Application-only and merged Compose configuration | PASS: `docker compose ... config -q` |
| Collector configuration | PASS: running 0.162.0 `validate --config` |
| Prometheus configuration and alert tests | PASS: running 3.15.0 promtool check/test |
| Prometheus application metric query | PASS: `election_poll_total{service_name="election-bot"}` returned series |
| Loki native OTLP ingestion | PASS: application log with trace ID returned |
| Tempo correlated trace retrieval | PASS: new simulation trace `ca36f1062eecf7b971846896a983ebf5` retrieved, with its newly ingested correlated Loki logs |
| Grafana provisioning and readiness probe | PASS: verifier checked source/dashboard UIDs and probe success |
| `/status` regression tests | PASS: configured source, no publication, current/stale synchronization, publication delay and publication timestamp |

`go test ./...` and `go test -race ./...` passed after the `/status` change: 64 tests across 19 packages. `make lint` (including `go vet ./...`), `make build`, `make simulate`, and `go mod verify` also passed. Backend versions: Collector 0.162.0, Prometheus 3.15.0, Grafana 13.2.3, Tempo 3.1.0, Loki 3.7.8, probe exporter 0.29.0. Docker daemon access was initially denied but became available after permissions changed; Docker checks were subsequently executed. The image and live telemetry stack were verified before the final `/status` change; the changed Go source was rebuilt/tested natively, while Compose definitions were unchanged.

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

## Independent final review

A fresh-context reviewer inspected `1704bf4..64b312a`. Five Important regressions were reproduced before fixing: unchanged-200 stale-warning recovery, old-generation terminal delivery failure, disabled transport consuming pending work, permanent command replies poisoning the shared command stream, and concurrent Retry-After cooldown bypass. Each permanent fix has a regression observed RED then GREEN. The polling finding was upgraded from Minor because recovery and latency are required behavior: transient fetches now have at most three attempts with cancellable exponential jitter; missing/rate-limited endpoints and explicit Retry-After do not receive immediate retries. Subsequent cadence uses the accepted counting/final status. A custom-port healthcheck regression was also fixed.

Final validation before the `/status` follow-up passed `go test ./...` (60 tests, 19 packages), `go test -race ./...`, `make lint` (including `go vet ./...`), `make build`, `make simulate`, and `go mod verify`. The `/status` follow-up adds regression cases for simulation/official source labels, no published data, fresh and stale synchronization, publication delay and publication timestamp; its focused tests and final full-suite/race results are recorded below. Both Compose configurations validated and the rebuilt image ran. Collector validation and Prometheus configuration/five alert rule checks and rule tests passed. Actual metrics, new correlated logs/traces, Grafana datasource/dashboard provisioning, and readiness were queried successfully.

The `/status` gap is fixed: it now displays configured source mode, last synchronization time/freshness, publication time/freshness, and consecutive failures. If no snapshot has been published, it reports that it is waiting for data. Regression coverage includes no publication, fresh results, and stale/delayed results. A committed-file scan found no token or credential values; the tracked `.env.example` contains only empty secret fields and `.env` is ignored.

## Remaining work before production

- Run a Telegram delivery smoke test with a properly configured bot. No real token/account was supplied or used in validation.
- Retrieve and verify the official TSE EA20 presidential runoff contract and EA11 production paths/configuration.
- Verify official national finalization and elected-candidate confirmation semantics; never infer a winner from vote totals or 100% progress.
- Complete production readiness validation before October 25, 2026. Official mode must continue failing closed until the contract and status mappings are reviewed and tested.

No live Telegram delivery or official runoff operation has been validated. Fake-server Telegram API tests, first-round simulation fixture parsing, and the offline recovery simulation do not establish those claims. The tracked `.env.example` contains empty credential fields; the local `.env` is ignored. A scan of committed files found no token/credential values.

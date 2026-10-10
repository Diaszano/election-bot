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

`go test -race ./...` passed across all packages, including the executable shutdown test. The final suite passed 60 tests across 19 packages; race detection and lint were rerun after review fixes. Backend versions: Collector 0.162.0, Prometheus 3.15.0, Grafana 13.2.3, Tempo 3.1.0, Loki 3.7.8, probe exporter 0.29.0. Docker daemon access was initially denied but became available after permissions changed; Docker checks were subsequently executed.

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

Final validation after these changes: `go test ./...` (60 tests, 19 packages), `go test -race ./...`, `make lint` (including `go vet ./...`), `make build`, `make simulate`, and `go mod verify` passed. Both Compose configurations validated and the rebuilt image ran. Collector validation and Prometheus configuration/five alert rule checks and rule tests passed. Actual metrics, new correlated logs/traces, Grafana datasource/dashboard provisioning, and readiness were queried successfully.

Deferred Minor: `/status` currently reports synchronization freshness and failures but omits source mode and publication-delay detail. Main results include those distinctions. The reviewer declined to judge verified runoff/winner semantics and public Telegram delivery: the closed production gate and fake-server testing remain the explicit boundary, with costs of unavailable official monitoring and unverified live-account behavior respectively.

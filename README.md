# election-bot

A Go modular monolith for monitoring Brazil's 2026 presidential runoff and delivering Portuguese Telegram updates. Module: `github.com/Diaszano/election-bot`.

**Current operational boundary:** simulation is implemented and validated. Official production mode deliberately refuses startup until the EA20 runoff schema, production paths and national status mappings are verified. Captured official TSE first-round simulation data is a parser fixture, never live runoff evidence. See [TSE contract](docs/tse-contract.md) and [validation ledger](docs/validation.md).

## Features

- One editable result message per private subscriber plus separate 25/50/75/90/95/100% section-progress alerts.
- SQLite snapshots, validators, synchronization status, subscriber preferences, main message IDs, durable outbox and command offset.
- Independent polling/publishing, conditional HTTP requests, stale-CDN rejection and opaque generation IDs.
- Idempotent command state; active `/start` preserves generation/baseline. Groups/channels are ignored.
- Durable retry state, Telegram 429 handling, deleted-message recreation, blocked-chat deactivation and stale indicators.
- Offline recovery self-check; fake HTTP integration tests; SIGTERM and telemetry outage tests.
- JSON slog, correlated OpenTelemetry traces/logs and business metrics. Collector, Prometheus, Tempo, Loki, Grafana and readiness probing.

## Prerequisites and versions

Install the pinned linter with `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2`.

Go 1.27.0; Docker Engine and Compose for the stack; Python 3 for its telemetry verifier; golangci-lint 2.13.2 for `make lint`. SQLite driver 1.60.1; OTel core/SDK 1.47.0, log exporter 0.23.0, otelslog 0.21.0, otelhttp 0.72.0. Exact transitive versions are in `go.mod` and `go.sum`. Images: Collector 0.162.0, Prometheus 3.15.0, Grafana 13.2.3, Tempo 3.1.0, Loki 3.7.8, blackbox exporter 0.29.0. Builder/runtime images are digest-pinned.

## Quick start

```sh
make test
make simulate
make build
./bin/election-bot
```

Defaults use fictional local simulation, disabled Telegram and disabled OTLP, with `data/elections.db`. This background simulation does not create a subscriber automatically. `make simulate` runs a finite recovery scenario with a recording sender and a temporary database. It never calls Telegram, even if Telegram is enabled in the environment. Both modes prominently label synthetic results.

Go does not load `.env` files automatically. Use your environment manager, or export values from a trusted local file:

```sh
cp .env.example .env
set -a
. ./.env
set +a
make run
```

Shell sourcing executes the file: use only a file you control. Do not commit `.env`.

## Telegram setup

1. Open Telegram's official `@BotFather`, run `/newbot` and retain the token privately.
2. Set `TELEGRAM_ENABLED=true` and `TELEGRAM_BOT_TOKEN` in your local environment or `.env` for Compose.
3. Start election-bot and open a private conversation with your bot. Use `/start`.
4. Commands: `/agora`, `/status`, `/parar`, `/help`. All bot replies are Brazilian Portuguese.

BotFather accepts `/setcommands` with these command descriptions:

```text
start - Receber atualizações
agora - Ver resultado atual
status - Ver situação do monitoramento
parar - Parar atualizações
help - Mostrar ajuda
```

New subscribers begin at current progress and receive subsequent milestone crossings, not historical push alerts. Their main message includes the latest reached milestone. Re-subscription preserves milestone history. A 100% alert does not announce a winner.

Main edits are deduplicated by desired presentation revision. Separate alerts use durable at-least-once delivery: an accepted Telegram send followed by an ambiguous timeout or crash before database acknowledgement can duplicate a push. No transaction can make Telegram and SQLite commit atomically. Retries remain durable, and operator-visible permanent failures are retained. `/parar` cancels queued work; an already in-flight send can still arrive.

## Configuration

See the complete [.env.example](.env.example). Duration values use Go syntax (`10s`, `5m`, `1h`). Mandatory settings are validated before startup.

| Settings | Defaults / behavior |
|---|---|
| `APP_NAME`, `APP_ENV`, `APP_LOG_LEVEL`, `APP_TIMEZONE` | election-bot, development, INFO, America/Sao_Paulo; DEBUG/INFO/WARN/ERROR |
| `ELECTION_YEAR`, `ELECTION_ROUND`, `ELECTION_CODE` | 2026, 2, 6258; other identities rejected |
| `DATA_SOURCE_MODE` | simulation; official mode fails closed with a contract-verification error |
| `TELEGRAM_ENABLED`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_POLL_TIMEOUT` | false, empty, 30 seconds; enabled transport requires token |
| `TSE_BASE_URL`, `TSE_ENVIRONMENT`, `TSE_RESULTS_URL` | official host/environment, explicit results override; never silently enable an unverified endpoint |
| `TSE_POLL_INTERVAL`, `TSE_PRE_COUNTING_INTERVAL`, `TSE_FINAL_INTERVAL` | 10s, 5m, 1h |
| `TSE_HTTP_TIMEOUT`, `ELECTION_COUNTING_START` | 5s, 2026-10-25T17:00:00-03:00 operational window |
| `SYNC_STALE_AFTER`, `PUBLICATION_DELAYED_AFTER` | 2m, 5m while counting |
| `NOTIFICATION_INTERVAL`, `SHUTDOWN_TIMEOUT` | 1s, 10s |
| `SQLITE_PATH`, `HTTP_LISTEN_ADDRESS` | data/elections.db, :8080; Compose forces /data/elections.db and :8080 |
| `OTEL_ENABLED`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL` | false, http://localhost:4317, grpc; Compose override enables Collector export |
| `OTEL_SERVICE_NAME`, `OTEL_SERVICE_NAMESPACE` | election-bot, elections; renaming rejected |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG` | parentbased_traceidratio, 1.0; ratio must be in [0,1] |
| `GRAFANA_ADMIN_PASSWORD` | Required for observability Compose; supply your own |

## Docker

```sh
docker compose up -d --build
curl -f http://127.0.0.1:8080/health/ready
```

For the full stack, configure `GRAFANA_ADMIN_PASSWORD` in `.env`, then:

```sh
docker compose -f compose.yaml -f compose.observability.yaml up -d --build
```

The application runs as UID 65532 in a distroless runtime; CA certificates and timezone data are available. The named `election-data` volume retains SQLite/WAL/SHM together. One replica only. Application startup does not depend on telemetry availability. Operator ports bind to localhost.

Grafana: http://127.0.0.1:3000, user `admin`, your configured password. Automatically provisioned dashboards: **Election Bot — Overview** and **Election Bot — Telemetry**. Prometheus: localhost:9090; Tempo: localhost:3200; Loki: localhost:3100.

To verify real telemetry and correlation, emit a fresh simulation trace and query the stack:

```sh
OTEL_ENABLED=true OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4317 make simulate
# Export the same Grafana password used by Compose:
export GRAFANA_ADMIN_PASSWORD
sh deployments/docker/verify-telemetry.sh
```

The verifier requires application metrics in Prometheus, correlated logs in Loki, retrieval of their trace ID in Tempo, Grafana data sources/dashboards and a successful readiness probe. It fails when signals are absent.

```sh
make docker-logs
make observability-down   # Stops containers; keeps persistent volumes.
```

Do not use `down -v` unless you intend to destroy subscriber and delivery state. `make clean` removes build outputs only.

## Official TSE mode

TSE confirms election 6258 on October 25. Official integration remains gated: changing `DATA_SOURCE_MODE=official` produces an explicit startup error. Do not change the guard alone. First retrieve EA20, verify national runoff status/winner fields, resolve the production URL through EA11, review DTO mappings and add correct-phase/identity/status contract tests. First-round `e=s`/`st=2º turno` does not mean an elected runoff winner.

The independently tested TSE adapter decodes the captured official simulation schema, validates identity/phase, uses ETag/Last-Modified, honors cache minima, accepts 304 only for committed validators and classifies 404/429/block/transient responses. It never substitutes simulation after a production failure. Fixture provenance and hashes are committed.

## Architecture and directory structure

Dependencies flow adapters → application → domain; the composition root constructs concrete adapters. Domain imports only the standard library. Business decisions run in application/domain; SQLite implements a focused transaction callback.

```text
cmd/election-bot/             Binary and composition entry
internal/domain/              Election, subscription, notification
internal/application/         Ports, use cases, presentation/telemetry API
internal/adapters/inbound/    Telegram commands, scheduler, health
internal/adapters/outbound/   TSE, Telegram, SQLite, simulation
internal/platform/            Config, observability, lifecycle
migrations/                   Embedded SQL schema
test/fixtures/tse/            Unmodified official simulation artifacts
test/integration/             Recovery and executable shutdown tests
deployments/                  Collector, Prometheus, Grafana, Tempo, Loki
docs/decisions/               Seven architecture decisions
```

See [architecture](docs/architecture.md), [observability](docs/observability.md) and [ADRs](docs/decisions/).

## Tests and tooling

```sh
go test ./...
go test -race ./...
go vet ./...
make lint
make simulate
```

Tests use temporary SQLite databases and fake HTTP/OTLP servers; no token or real Telegram account is needed. The executable shutdown test builds the binary and tests SIGTERM while telemetry is unreachable. Supported Make targets are listed by `make help`. Current verification evidence is in [docs/validation.md](docs/validation.md).

## Troubleshooting, recovery and security

- No results yet: readiness depends on local storage, not publication. `/status` distinguishes last synchronization and failures; `/agora` keeps the last valid snapshot.
- HTTP 404: verify the documented URL; the worker applies a 15-minute cooldown. Rate-limit/block responses wait at least ten minutes or Retry-After.
- Telegram failures: inspect `outbox.state`, `attempts`, `last_error`, `due`. Transient failures keep retrying in bounded scheduled groups. Blocked/invalid chats become inactive with undeliverable rows. Payload failures retain rows as failed for inspection.
- For an operator replay, first correct the payload/configuration, stop the application and back up SQLite. Update only reviewed failed rows for an active matching subscriber generation to `state='pending', attempts=0, last_error='', due=0`. Never replay delivered milestone rows; that deliberately creates duplicates. Restart the single instance.
- SQLite backup: use the SQLite backup API or `sqlite3 /path/elections.db '.backup /safe/backup.db'`. Do not copy only the live `.db` file and omit WAL. Protect the database: it contains private chat identifiers and message state.
- Keep tokens/passwords outside source control. Structured signals contain neither identifiers nor message bodies/token-bearing Telegram URLs. Do not expose Grafana or unauthenticated backend APIs publicly without authentication/TLS.
- A restored backup can replay deliveries newer than the backup; reconcile externally delivered alerts before replay. Horizontal scaling requires replacing the single-instance scheduling/persistence design, not adding a second replica against this volume.

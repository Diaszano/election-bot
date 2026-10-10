# election-bot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Do not begin implementation until the user approves the written documents and chooses the execution method.

**Goal:** Deliver a tested, persistent and observable Go Telegram bot for Brazil's official 2026 presidential runoff, including deterministic offline simulation.

**Architecture:** One modular monolith with election, subscription and notification domain packages. Application use cases depend on focused ports; SQLite atomically accepts snapshots and queues work. Independent poll/notification workers recover after restart, and infrastructure is assembled in one composition root.

**Tech Stack:** Go 1.27.0 baseline; standard HTTP, database/sql and slog; maintained pure-Go SQLite driver; official OpenTelemetry SDKs/OTLP gRPC exporters and maintained otelslog/HTTP instrumentation; Docker Compose, Collector, Prometheus, Grafana, Tempo, Loki and a readiness probe exporter. Exact dependency/image versions are resolved and pinned in Task 1/10, with compatibility evidence recorded.

**Spec:** [2026-10-10-election-bot-design.md](../specs/2026-10-10-election-bot-design.md). Read it before executing any task; it defines source verification gates, timing, delivery semantics and acceptance criteria.

## Global Constraints

- Go module `github.com/Diaszano/election-bot`; binary, image and Compose service `election-bot`.
- `service.name=election-bot`; `service.namespace=elections`.
- English code/documentation/logs; Brazilian Portuguese Telegram commands/messages.
- Election 2026, round 2, code 6258, BR, President; no guessed official schema/status/endpoint or candidates.
- Active polling `10s`, pre-counting `5m`, finalized `1h`; operational counting window `2026-10-25T17:00:00-03:00`.
- TSE timeout `5s`, 404 cooldown `15m`, rate-limit cooldown at least `10m`; Retry-After may extend waits.
- Three transient attempts per cycle, full jitter base `1s`, cap `30s`; durable notification rescheduling capped at `5m`.
- Telegram limiter `20` requests/second; per-chat interval at least `1s`, milestone sends at least `3s`.
- Milestones 25, 50, 75, 90, 95, 100; separate alerts use at-least-once delivery, never silently discarded.
- SQLite WAL, foreign keys, busy timeout `5s`, durable writes, one initial connection; single replica.
- Active synchronization stale after `2m`, publication delayed after `5m`; no freshness alerts outside active counting.
- Graceful shutdown deadline `10s`; Compose stop grace `15s`.
- No secrets/chat IDs/message bodies in telemetry, no brokers, no unrequested settings UI, no production simulation fallback.

## Review Focus

1. A newer revision arrives while an earlier Telegram edit completes: the newest revision remains queued (Task 6).
2. A malformed 200 includes new cache validators: the next fetch cannot return 304 against uncommitted results (Task 4/6).
3. A subscriber stops and starts while delivery is in flight: old completion cannot overwrite the new generation (Task 3/6).
4. Official totals/progress decrease in a correction: accept newer valid data without resetting delivered milestones (Task 2/6).
5. HTTP instrumentation sees a token-bearing Telegram URL or response error: no exported signal contains the token or chat identity (Task 5/8).

## File ownership map

| Files | Responsibility |
|---|---|
| `cmd/election-bot/main.go` | Composition root, process entry, health-check subcommand |
| `internal/domain/election/{snapshot,percentage,status}.go` | Invariants, canonical comparison, milestones |
| `internal/domain/subscription/subscription.go` | Active state, generation and preferences |
| `internal/domain/notification/delivery.go` | Pending work and retry state |
| `internal/application/ports/{results,state,notifications,clock}.go` | Focused external dependencies and transaction results |
| `internal/application/usecases/{monitoring,subscriptions,publishing}.go` | Explicit orchestration/use cases |
| `internal/application/services/format.go` | Deterministic Portuguese presentation |
| `internal/adapters/outbound/{tse,telegram,sqlite,simulation}/` | External contracts, persistence, synthetic source |
| `internal/adapters/inbound/{telegram,scheduler,health}/` | Commands, worker scheduling, health handlers |
| `internal/platform/{config,observability,persistence,lifecycle}/` | Configuration, SDK composition, database opening, process coordination |
| `migrations/{embed.go,001_initial.sql}` | Embedded transactional schema migration |
| `test/{fixtures,integration}/` | Provenance-tagged samples, executable recovery simulation |
| `deployments/{docker,otel,prometheus,grafana,tempo,loki}/` | Runtime/telemetry configurations and checks |
| `docs/{architecture,observability,tse-contract,validation}.md`, `docs/decisions/` | Contracts, diagrams, decisions, runbooks and evidence |

Each production Go file gets focused tests beside it unless an integration scenario crosses packages. Create only files serving a task; the baseline tree is a guide, not a requirement for empty packages.

## Task 1: Verified contracts and reproducible configuration

**Files:** Create `go.mod`, `go.sum`, `.env.example`, `.gitignore`, `.dockerignore`, `internal/platform/config/config.go`, `internal/platform/config/config_test.go`, `docs/tse-contract.md`, `docs/validation.md`, `test/fixtures/README.md`.

**Interfaces:** Produce `config.Load() (Config, error)`. Config holds validated election identity, URLs/source mode, Telegram enablement/token, durations, sampling/export settings, SQLite path, timezone and listen address. Values/defaults come from the spec; secrets must not be printable in configuration diagnostics.

- [ ] Retrieve EA20, EA11/live configuration and current official dependency manifests; record field/path/status mappings, source URLs, document dates and caching observations. Do not probe guessed results paths. If retrieval is blocked, record the blocker and preserve the spec's fail-closed official-mode rule.
- [ ] Resolve Go 1.27-compatible SQLite/OTel packages and supported lint release from primary sources. Pin exact versions and run `go mod download`, `go mod verify`; record output and required network escalations. Pin separately versioned log SDK/bridge modules from their declared dependencies.
- [ ] Write `TestLoadRejectsInvalidConfiguration` with table cases: negative/zero intervals, invalid timezone, sampler outside [0,1], malformed URL, wrong application/service identity, unsupported mode/protocol and enabled Telegram without a token. Assert each returns an error and does not contain the supplied test token. Write `TestOfflineSimulationNeedsNoToken` asserting valid defaults and disabled Telegram are accepted.
- [ ] Run `go test ./internal/platform/config -v` and observe the expected failure before implementation.
- [ ] Implement `Load` using standard environment parsing; add all original variables and the spec's operational settings to `.env.example`. Implement explicit OTLP/Telegram enablement so application-only/offline runs need neither external backend nor token.
- [ ] Run the configuration tests to PASS; commit only this task's files with `build: establish verified contracts and configuration`.

## Task 2: Domain invariants and deterministic formatting

**Files:** Create the domain files in the ownership map and their `_test.go` companions; `internal/application/services/format.go`, `format_test.go`; labeled fixture data once schema verification permits it.

**Interfaces:** Produce `election.NewPercentage(string) (Percentage, error)`, `Percentage.String() string`, `Percentage.Ratio() float64`, `election.Snapshot.Validate() error`, `Snapshot.MeaningfullyEqual(Snapshot) bool`, `Snapshot.ReachedMilestones() []int`, `Snapshot.Difference() (int64, Percentage, error)`. Snapshot includes identity, candidate results, official vote categories, source progress, publication time/version, internal status, source mode and explicit confirmed candidate if present. Produce `subscription.Subscription` (chat ID, active flag, generation, preferences, baseline), `notification.Delivery` (ID/election/chat/generation/kind/revision/milestone/attempts/due/state) and `services.FormatResults(election.Snapshot, Freshness, *time.Location) string`, `FormatMilestone(election.Snapshot, int, *time.Location) string`. Freshness contains stale/delayed flags and last synchronization; it is an application presentation type.

- [ ] Write percentage tests with these pinned assertions:

```go
func TestPercentageBounds(t *testing.T) {
    for _, raw := range []string{"-0.01", "100.01", "NaN", ""} {
        if _, err := NewPercentage(raw); err == nil { t.Fatalf("accepted %q", raw) }
    }
    p, err := NewPercentage("87.42")
    if err != nil || p.String() != "87.42" { t.Fatalf("percentage: %v %v", p, err) }
}
```

- [ ] Write tests asserting progress 24.99 reaches none, 25 reaches `[25]`, 95 reaches `[25,50,75,90,95]`; negative votes/overflow fail; candidate order does not change meaning; newer corrections may decrease progress; 100% without official confirmation formats no winner. Add golden Portuguese output with fictional candidates, escaping, vote grouping, percentage decimals, timezone conversion and simulation labels.
- [ ] Run `go test ./internal/domain/... ./internal/application/services/...` to FAIL before implementation.
- [ ] Implement these types/behaviors without adapter imports. Keep official raw status mapping out of the domain and validate documented category relationships only.
- [ ] Run the same tests to PASS; commit with `feat: model election and notification rules`.

## Task 3: Transactional SQLite state and outbox

**Files:** Create `migrations/embed.go`, `migrations/001_initial.sql`, `internal/platform/persistence/sqlite.go`, `internal/adapters/outbound/sqlite/{store,subscriptions,snapshots,outbox}.go` and tests; application port files from the ownership map.

**Interfaces:** Produce `persistence.Open(context.Context, string) (*sql.DB, error)` and `sqlite.New(*sql.DB) *Store`. Define focused ports grouped by consumer: `ResultsProvider.Fetch(context.Context, Validators) (FetchResult, error)`; `Clock.Now() time.Time`; `NotificationSender.Send(context.Context, int64, string) (int64, error)` and `Edit(context.Context, int64, int64, string) error`. Validators contains ETag/LastModified; FetchResult contains optional snapshot, validators, unchanged flag and earliest next fetch time.

Persistence method contracts:

- `WithSnapshotTransaction(context.Context, func(SnapshotTransaction) error) error` exposes a focused transaction to the application; the callback reads prior state/subscribers, applies domain comparison and milestone rules, then stores the snapshot/synchronization and enqueues computed deliveries atomically. `SnapshotTransaction` provides `Latest() (election.Snapshot, error)`, `Subscriptions() ([]subscription.Subscription, error)`, `Save(election.Snapshot, Validators, time.Time) error` and `Enqueue([]notification.Delivery) error`. Methods use the enclosing transaction context. No HTTP calls or domain decision rules live in the SQLite adapter.
- `RecordSynchronization(context.Context, SyncUpdate) error`, `GetLatestResults(context.Context) (election.Snapshot, error)`, `GetMonitoringStatus(context.Context) (MonitoringStatus, error)`. SyncUpdate holds success/failure/next-fetch metadata; MonitoringStatus includes accepted validators and snapshot availability.
- `Subscribe(context.Context, int64, time.Time) error`, `Unsubscribe(context.Context, int64, time.Time) error`, `GetSubscription(context.Context, int64) (subscription.Subscription, error)`.
- `Pending(context.Context, time.Time, int) ([]notification.Delivery, error)`, `Complete(context.Context, notification.Delivery, int64, time.Time) error`, `Reschedule(context.Context, notification.Delivery, time.Time, string) error`, `MarkUndeliverable(context.Context, notification.Delivery, string) error`.
- `QueueFreshness(context.Context, bool, bool) error`, `GetCommandOffset(context.Context) (int64, error)`, `SetCommandOffset(context.Context, int64) error`.

Split these into small interfaces where consumed; Store implements them all. Define `ports.ErrNoSnapshot`/`ErrNotFound`; translate SQL errors explicitly. HTTP work never occurs inside these methods.

- [ ] Write temporary-database tests for transactional migrations/rollback, foreign keys, milestone unique keys, concurrent command/poll writes, snapshot/outbox atomicity, repeated subscribe/unsubscribe, preservation of main IDs/history and reopen recovery. Assert a stale subscription generation cannot complete new work. Assert reopening after a simulated aborted snapshot transaction exposes neither its snapshot nor its jobs.
- [ ] Run `go test ./internal/adapters/outbound/sqlite/... ./internal/platform/persistence/...` to FAIL.
- [ ] Implement embedded migrations, schema constraints and parameterized repositories; connect migration and DB readiness. Encode timestamps consistently and store snapshot provenance/validators together.
- [ ] Run these tests plus `go test -race ./internal/adapters/outbound/sqlite/...` to PASS; commit with `feat: persist snapshots subscriptions and durable outbox`.

## Task 4: Verified TSE adapter and synthetic provider

**Files:** Create `internal/adapters/outbound/tse/{client,dto,parse}.go`, tests; `internal/adapters/outbound/simulation/provider.go`, tests; `test/fixtures/tse/` verified-schema fixture files and provenance notes.

**Interfaces:** Implement `ResultsProvider`; `tse.New(*http.Client, Config) (*Provider, error)` accepts verified URL/configuration inputs; `tse.Parse([]byte, election.Identity, *time.Location) (election.Snapshot, error)` translates verified DTOs. `simulation.New([]election.Snapshot) *Provider` implements the same fetch contract with deterministic steps. Classified fetch errors carry retryability/HTTP status/cooldown without leaking private input.

- [ ] Create synthetic EA20 fixtures only after mapping is documented; include optional/missing fields, election/phase mismatch, zero votes, counting, finalized-without-winner, confirmed-winner, malformed numeric fields and corrections. Label them synthetic, not captured official results.
- [ ] Write `httptest` cases for ETag, Last-Modified, valid 304, 304 without cached representation, malformed 200/new validators, bounded body size, timeout, cancellation, 404/no immediate retry, 429 Retry-After and 5xx recovery. Assert parsing does not mutate cached validators before acceptance.
- [ ] Run `go test ./internal/adapters/outbound/tse/... ./internal/adapters/outbound/simulation/...` to FAIL.
- [ ] Implement configurable verified paths, bounded decode, conditional requests and DTO conversion. Keep retry scheduling in the application/worker, not duplicate retry loops in the client. Reject schema uncertainty with an explicit official-mode error; never invent fields to satisfy tests.
- [ ] Run tests to PASS; record any still-unverified live endpoint separately; commit with `feat: add verified TSE and simulation providers`.

## Task 5: Telegram transport and command adapter

**Files:** Create `internal/adapters/outbound/telegram/{client,errors,limiter}.go`, tests; `internal/adapters/inbound/telegram/{commands,updates}.go`, tests; subscription use-case file and tests.

**Interfaces:** Implement `NotificationSender` via `telegram.New(*http.Client, Config) *Client`. Produce outbound `GetUpdates(context.Context, int64, int) ([]Update, error)` and `GetMe(context.Context) (BotIdentity, error)`; outbound DTOs do not enter the domain. Command adapter `Handler.Handle(context.Context, Command) error` depends on `SubscribeUser`, `UnsubscribeUser`, `GetLatestResults`, `GetMonitoringStatus` and sender; Command is a local inbound type containing chat, parsed command and update ID. Use cases expose those names with `(context.Context, int64) error` for subscription actions and the Task 3 getter signatures for reads.

- [ ] Write fake-HTTP tests for send/edit/getUpdates, 429 retry-after, malformed envelopes, HTTP timeout, message-not-modified, missing main message, blocked/invalid chat and ambiguous network failure. Check error strings never contain a sentinel token or chat ID. Test limiter cancellation and server delay precedence.
- [ ] Write handler tests for all five Portuguese commands, repeated `/start`, unsubscribe, correct/wrong bot suffix, unknown commands, non-command input and command replay after persisted offset. Assert distinct subscribers do not affect one another.
- [ ] Run `go test ./internal/adapters/outbound/telegram/... ./internal/adapters/inbound/telegram/... ./internal/application/usecases/...` to FAIL for new functionality.
- [ ] Implement standard-library Bot API JSON transport with escaped HTML presentation, sanitized errors and typed error classification. Provide Portuguese help/status text and explicit waiting/stale/simulation messages. Use global/per-chat limiter state shared by command replies and notification delivery.
- [ ] Run tests to PASS; commit with `feat: implement Telegram transport and subscription commands`.

## Task 6: Monitoring and independent durable publisher

**Files:** Create `internal/application/usecases/{monitoring,publishing}.go`, tests; `internal/adapters/inbound/scheduler/{poller,publisher}.go`, tests.

**Interfaces:** Produce constructor-injected `Monitor.PollElectionResults(context.Context) error`, `Monitor.ProcessElectionSnapshot(context.Context, FetchResult) error`, `Publisher.PublishResultUpdates(context.Context) error`, `EvaluateMilestoneAlerts(previous, current election.Percentage, baseline int) []int`. Domain supplies threshold rules; Store provides transactional deduplication. Workers expose `Run(context.Context) error`, consume Clock and cancelable timers; one iteration can be driven explicitly by tests.

- [ ] Write tests proving unchanged/304 polls create no duplicate work but retry pending deliveries; crossing 24→76 queues 25/50/75 once; a subscriber joining at 76 gets no historical alerts; retry exhaustion retains jobs; restart completes them; blocked chats become inactive with visible terminal state.
- [ ] Write controlled-interleaving tests: a new revision arrives while edit is in flight; old completion leaves new work pending; stop/start invalidates old generation; newer official correction preserves delivered milestones; stale/recovered transitions each queue only one edit. Simulate the send-success/DB-ack crash and assert the documented possible retry duplicate, never a false exactly-once claim.
- [ ] Write scheduling tests for `10s`/`5m`/`1h`, cache minimums, 404 `15m`, rate-limit `10m`, bounded jitter, context cancellation, no concurrent polls and finalization restored after restart.
- [ ] Run `go test ./internal/application/usecases/... ./internal/adapters/inbound/scheduler/...` to FAIL.
- [ ] Implement use cases/independent worker loops using Task 3 ports. Generate IDs deterministically for milestone rows; coalesce main updates by desired revision, keep milestones distinct. Store delivery acknowledgements only after remote success, and retain failures with due time/classification. Avoid holding DB locks while calling Telegram.
- [ ] Run focused tests plus race detection to PASS; commit with `feat: monitor elections and recover durable notifications`.

## Task 7: Executable lifecycle, health and offline simulation

**Files:** Create `cmd/election-bot/main.go`, `internal/platform/lifecycle/run.go`, tests; `internal/adapters/inbound/health/handler.go`, tests; `test/integration/simulation_test.go`, `lifecycle_test.go`; initial Makefile.

**Interfaces:** Produce `lifecycle.Run(context.Context, config.Config) error`, `health.New(func(context.Context) error) http.Handler`; main uses signal cancellation and explicit constructors. Binary accepts `--simulate` for a finite self-check and `--healthcheck` for a liveness HTTP probe without third-party shell utilities.

- [ ] Write deterministic simulation using a temporary persistent SQLite DB and recording sender: initial result, subscriber, multi-threshold progress, edits, unchanged replay, component restart, pending delivery recovery, upstream failure/stale transition/recovery. Assert expected main-message count, edit count, per-threshold durable identity and no duplicate jobs on replay.
- [ ] Write health tests for initialized DB with unpublished upstream (ready), startup migration failure (not ready), and operational liveness. Write executable SIGTERM integration test asserting exit within `10s` and persistent work recovered on reopen.
- [ ] Run `go test ./test/integration/... ./internal/platform/lifecycle/... ./internal/adapters/inbound/health/...` to FAIL.
- [ ] Wire independent workers, token-free recording simulation, HTTP server, command offset and orderly cancellation/shutdown. Create Make targets for build/run/test/test-race/fmt/simulate/clean; clean must preserve data.
- [ ] Run the integration tests, `go build -o bin/election-bot ./cmd/election-bot`, `make simulate` to PASS; commit with `feat: run election-bot with health and deterministic simulation`.

## Task 8: All three telemetry signals with redaction tests

**Files:** Create `internal/platform/observability/{providers,metrics,logging,http}.go`, tests; instrument application use cases and outbound adapters; `test/integration/telemetry_test.go`.

**Interfaces:** Produce `observability.Setup(context.Context, config.Config, string) (*Runtime, error)` and `Runtime.Shutdown(context.Context) error`. Runtime exposes configured tracer/meter/logger for constructor injection; domain stays unaware. Application may depend on OTel API types; SDK/platform imports remain in composition/adapters.

- [ ] Start local fake OTLP gRPC receivers for traces/metrics/logs. Write tests that a poll and notification export each signal, child spans share trace IDs, slog records correlate, counters/gauges/histograms contain expected values and resources match `election-bot`/`elections`. Assert no span/log/metric contains sentinel token, chat ID or content.
- [ ] Test unreachable backend while polling/publishing remains functional, invalid sampler configuration, cancellation and bounded provider shutdown. Assert unchanged polls do not emit noisy INFO result logs.
- [ ] Run telemetry tests to FAIL.
- [ ] Configure compatible SDKs/exporters, bridge JSON and OTLP logs, implement required metrics plus alert-state gauges and privacy-safe spans. Avoid raw Telegram URL HTTP spans; add safe logical spans and operation labels. Use bounded batches and background export.
- [ ] Run tests to PASS and record actual received OTLP payload evidence; commit with `feat: instrument election-bot with safe OpenTelemetry signals`.

## Task 9: Collector, backends, dashboards and alerts

**Files:** Create `deployments/otel/collector.yaml`, `deployments/prometheus/{prometheus,alerts,blackbox}.yaml`, `deployments/prometheus/alerts_test.yaml`, `deployments/tempo/tempo.yaml`, `deployments/loki/loki.yaml`, Grafana provisioning/data-source/dashboard YAML and `overview.json`, `telemetry.json`; `deployments/docker/verify-telemetry.sh`.

**Interfaces:** OTLP gRPC/HTTP receivers → Tempo OTLP, Collector Prometheus endpoint and Loki native OTLP HTTP. Grafana datasource UIDs `prometheus`, `tempo`, `loki`. The readiness probe exporter supplies `probe_success` and duration; dashboards use actual normalized service labels.

- [ ] Inspect supported component documentation for pinned Collector/Loki/Tempo/Prometheus/Grafana versions. Write configurations with memory limiter/batch, no deprecated exporters, Loki structured metadata and datasource trace/log correlation.
- [ ] Generate sample Prometheus text via the same Collector exporter in a test or accessible binary; record exact metric names/resource labels in validation notes. Author dashboards from that output, not assumed names. Include all panels listed in the spec; use Tempo datasource search rather than nonexistent trace metrics.
- [ ] Create alert rules and promtool tests asserting active-counting failure/staleness fires at prescribed durations, waiting/finalized freshness does not fire, and availability/export failure alerts remain enabled.
- [ ] Validate JSON, YAML, alert expressions/tests and supported configuration with installed tools. Record missing binaries explicitly and never equate syntax checks with component validation.
- [ ] Implement the live verification script: emit known simulation signals, query Prometheus for counters/gauges, Loki for correlated records, Tempo for a known trace and Grafana for datasource/dashboard UIDs. Bounded retries fail explicitly for absent signals; no container-only success.
- [ ] Commit with `feat: provision observability dashboards and actionable alerts`.

## Task 10: Docker deployment and developer tooling

**Files:** Create `Dockerfile`, `compose.yaml`, `compose.observability.yaml`, `.golangci.yml`; complete Makefile and env/docker-ignore files.

**Interfaces:** `docker compose up -d --build` runs application only; merged Compose runs application/Collector/Prometheus/Grafana/Tempo/Loki/probe exporter. Grafana binds `127.0.0.1:3000`; health binds configurable application address, internal data directory `/data`.

- [ ] Verify official Go builder/runtime and backend image versions/digests; pin them. Implement non-root multistage build, writable persistent directory, CA/timezone data and binary health check. Configure separate backend volumes and private network, optional OTLP export and required external Grafana credentials.
- [ ] Complete all requested Make targets, pinned golangci-lint setup and safe cleanup. Ensure application-only mode has no telemetry dependency and no token requirement when Telegram is explicitly disabled.
- [ ] Run `docker compose --env-file .env.example -f compose.yaml config -q` and merged `config -q` with a temporary nonsecret test Grafana password. Inspect resolved config for unexpected secret exposure; never print real tokens.
- [ ] If daemon access is authorized/available, run `make docker-build`, launch both modes and execute the Task 9 telemetry verifier. Otherwise record image/runtime/telemetry checks as NOT EXECUTED and continue Task 11/12.
- [ ] Commit with `build: package election-bot and observability deployments`.

## Task 11: Full verification and corrective work

**Files:** Modify only implementation/tests/configuration implicated by failures; update `docs/validation.md`.

- [ ] Run `gofmt` on Go files and `git diff --check`; confirm neither has outstanding changes/errors.
- [ ] Run `go mod verify`, `go build ./cmd/election-bot`, `go test ./...`, `go test -race ./...`, `go vet ./...`, `make lint`, `make simulate`; record exact commands, exit status, versions and any unavailable tools.
- [ ] Confirm architecture import boundaries using `go list` dependency data; domain imports contain none of the forbidden infrastructure/telemetry packages. Check fixtures and code for production use of fictional data and telemetry for sensitive labels.
- [ ] Fix observed failures at their root and rerun affected checks; run the full suite again only if new edits justify it. Do not replace assertions merely to make tests green.
- [ ] Verify restart/finalization/retry recovery with executable and fake transport. When Docker is accessible, execute image, live health, shutdown, all three telemetry paths, dashboard provisioning and alert checks. Keep environment-blocked evidence distinct from passing checks.
- [ ] Commit any fixes with `fix: resolve validation findings` and preserve a candid acceptance ledger.

## Task 12: Operating documentation and review delivery

**Files:** Create `README.md`, `docs/architecture.md`, `docs/observability.md`, `docs/decisions/0001-go.md` through `0007-single-instance.md`; complete `docs/tse-contract.md` and `docs/validation.md`.

- [ ] Write README with complete directory tree, implemented features, exact versions, BotFather/token setup, commands, local/Docker/observability execution, official-mode verification gates, simulation, tests, configuration and security/troubleshooting. Explain new-subscriber milestone baseline and duplicate risk after ambiguous Telegram sends.
- [ ] Add five architecture Mermaid diagrams and seven ADRs specified by the design. Explain backup/restore, pending/undeliverable row inspection and safe replay after correcting payload/configuration errors, subscriber deactivation, telemetry failure isolation, SQLite/single-instance limits and throughput lag.
- [ ] Document actual exported metric names/labels, dashboards, sampling, privacy, correlated traces/logs, alert runbooks and live verifier use. Link required primary-source contracts and fixture provenance.
- [ ] Check every documented command against implemented targets/options and available execution results. No placeholder sections, invented passing results or assertion that unexecuted Docker checks passed.
- [ ] Commit with `docs: document deployment recovery and validation`; submit the final tree, functionality, decisions, commands, test results and outstanding limitations for review. Integration/merge is a separate user decision.

## Execution handoff

The user has requested specification and plan preparation for final review, not selected an execution method. Recommend **Native** implementation in this session: tasks depend closely on shared domain and persistence contracts, and this keeps the interface decisions in one context. An independent whole-branch review follows. **Subagent-driven** execution is available if the user prefers independent per-task implementation/review at additional context cost. Do not spawn agents before the user chooses.

Self-review completed: every design section maps to a task; the five review-focus cases have explicit tests; cache-validator commit, pending-revision acknowledgement, subscription generation and transient retry retention share consistent persistence contracts. Official schema verification and live Docker telemetry remain explicit execution gates, not guessed successes.

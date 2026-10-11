# Verification ledger

Date: 2026-10-10 / 2026-10-11. Implementation branches: `feat/election-bot` (Telegram/core monolith) and `feat/whatsapp-integration` (WhatsApp channel extension). Architecture, specifications, and plans were approved before product implementation. Native execution used incremental commits and test-first package/regression checks.

## Executed evidence — Core monolith (`feat/election-bot`)

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

## Executed evidence — WhatsApp integration (`feat/whatsapp-integration`)

| Task / Check | Executed verification & evidence | Result |
|---|---|---|
| **Task 1**: Channel identity & presentation policy | Unit tests for `subscription.Identity`, WhatsApp plain-text/markdown rendering, and `DecideMain` 14m edit age window. Commit `da8a668`. | PASS |
| **Task 2**: Lossless schema migration | `migrateV2` tests upgrading v1 database to v2 with channel columns, preserving Telegram subscriptions/jobs/history, collision preflight keys, rollback coverage, and backup restoration. `migrations/001_initial.sql` verified completely unchanged. Commit `1c9b033`. | PASS |
| **Task 3**: Channel outbox & immutable attempts | SQLite repository tests for `ChannelDeliveries`, immutable attempt preparation (`PrepareAttempt`) persisting request IDs before network dispatch, generation guards, and retry deduplication. Commit `9f63992`. | PASS |
| **Task 4**: Shared subscription & command rules | Usecase tests verifying idempotent subscribe/unsubscribe across channels, Brazilian Portuguese command translations (`!start`, `!agora`, `!status`, `!parar`, `!help`), active `/start` baseline preservation. Commit `5ef4957`. | PASS |
| **Task 5**: Channel publisher isolation | Publisher tests verifying independent serial execution per channel, retry scheduling, pacing enforcement, and request ID reuse on identical payload retries. Commit `c5b4381`. | PASS |
| **Task 6**: Protected session storage & locking | Cross-process advisory lock tests (`whatsapp-session.db.lock`), path alias exclusion, 0/1/>1 stored device fail-closed selection, and owner-only (0600) permissions. Commit `93be272`. | PASS |
| **Task 7**: whatsmeow client & connection state | Adapter tests for `whatsapp.Client`, bounded connection state machine, exponential reconnect backoff, and transport error classification. Commit `709260c`. | PASS |
| **Task 8**: CLI pair & logout commands | CLI dispatch tests for `election-bot whatsapp pair` (verifying interactive terminal check and half-block QR generation) and `election-bot whatsapp logout` with exclusive session lock acquisition. Commit `c82f43f`. | PASS |
| **Task 9**: Private command authorization | Inbound handler tests verifying strict allowlist filtering (`WHATSAPP_ALLOWED_RECIPIENTS`), canonical JID normalization (`5511999999999@s.whatsapp.net`), and silent rejection of unlisted senders, groups, communities, and newsletters. Commit `13934e3`. | PASS |
| **Task 10**: Edit window & summary digest policy | Integration tests verifying message editing within 14 minutes (`MaxEditAge = 14*time.Minute`), automatic transition to summary digests (`WHATSAPP_DIGEST_INTERVAL=15m`) past 14m or upon edit failure, and deduplication of already accepted presentations (`ResolveAlreadyAccepted`). Commit `fc98217`. | PASS |
| **Task 11**: WhatsApp telemetry & privacy | OTLP metrics and span tests verifying bounded connection enums, counters, histograms, and complete absence of recipient phone numbers, JIDs, QR payloads, or message bodies in telemetry. Verified Overview and Telemetry Grafana dashboard panel queries. Commit `bf925e4`. | PASS |
| **Task 12**: Container operations & security | Compose configuration tests for UID 65532 non-root user, isolated `WHATSAPP_SESSION_DB_PATH`, exclusion of session DBs and secrets from Docker build contexts (`.dockerignore`). Commit `251efab`. | PASS |
| **Task 13**: Dual-channel recovery simulation | Integration recovery tests (`TestDualChannelRecovery`, `TestAmbiguousAcceptanceRestart`, `TestRetryExhaustedWorkRequeue`, `TestOfficialRunoffGateUnchanged`) proving that WhatsApp stalls or disconnects cannot block Telegram or polling, and restart restores pending deliveries. Commit `7330a9e`. | PASS |
| **Task 14**: Documentation & final review | Architecture decision records (`0008-channel-notifications.md`, `0009-whatsapp-sessions.md`, `0010-whatsapp-delivery-policy.md`), full documentation updates, plan completion, and full verification suite execution. | PASS |
| **Full test suite (`go test ./...`)** | 80 tests passing across 21 packages without cached regressions. | PASS |
| **Race detector (`go test -race ./...`)** | Zero data races detected across all domain, application, adapter, and integration packages. | PASS |
| **Static analysis (`go vet ./...`)** | Clean analysis across all packages. | PASS |
| **Code linting (`make lint`)** | golangci-lint 2.13.2 reported 0 issues. | PASS |
| **Binary compilation (`make build`)** | `go build -trimpath -o bin/election-bot ./cmd/election-bot` succeeded. | PASS |
| **Recovery simulation (`make simulate`)** | Deterministic simulation ran and passed (`election-bot simulation: PASS`). | PASS |
| **Dependency verification (`go mod verify`)** | All dependencies matched pinned module hashes. | PASS |
| **Pure-Go binary (`CGO_ENABLED=0 go build`)** | Built pure-Go binary without CGO dependencies. | PASS |
| **Docker Compose configuration** | `docker compose -f compose.yaml config -q` and merged `docker compose -f compose.yaml -f compose.observability.yaml config -q` validated cleanly. | PASS |
| **Formatting & Git whitespace** | `gofmt -l .` returned 0 unformatted files; `git diff --check` reported 0 whitespace errors. | PASS |

## Integration boundary and limitations

1. **No Real WhatsApp Account Pairing or Messaging**: No real WhatsApp phone number, credentials, or session keys were used during validation. All WhatsApp adapter behavior was validated against deterministic fake clients, simulated network connections, and offline session stores. whatsmeow is an unofficial WhatsApp Web client. Account restriction or suspension remains an inherent operational risk of automated messaging on WhatsApp, even at low volumes. The official WhatsApp Business Platform is the recommended long-term solution for official production messaging.
2. **No Real Telegram Delivery**: Real public Telegram delivery was not performed; API behavior is tested through authenticated HTTP mock servers.
3. **Official TSE Runoff Contract**: Official mode remains fail-closed by design (`DATA_SOURCE_MODE=official` halts startup). TSE EA20 contract documentation returned HTTP 403, and national runoff winner confirmation fields remain unverified against official production APIs.
4. **Schema Migration Integrity**: `migrations/001_initial.sql` was verified to be completely unmodified. Database migration from v1 to v2 is strictly non-destructive.
5. **Privacy and Secret Scanning**: Scanned all tracked files, commit history, and staging. Zero tokens, passwords, real recipient phone numbers, private JIDs, QR codes, or encryption keys are present in repository files. Tracked `.env.example` contains only placeholder values.

## Decisions and operational boundaries

- **Channel Isolation**: Polling worker commits updates to SQLite. Telegram and WhatsApp publisher workers poll their respective pending outbox queues asynchronously. A WhatsApp reconnect, rate limit, or network failure has zero impact on Telegram notifications or election polling.
- **14-Minute Edit Age Cutoff**: WhatsApp Web protocol does not permit editing messages older than 14 minutes. Outbox evaluation smoothly transitions to sending periodic summary digests governed by `WHATSAPP_DIGEST_INTERVAL` (default 15m).
- **At-Least-Once Delivery**: Network delivery followed by a crash before SQLite transaction commit can result in duplicate message delivery. Stable request IDs and presentation deduplication mitigate transport-level duplication.
- **Exclusive Session Locking**: `whatsapp-session.db.lock` enforces cross-process mutual exclusion via POSIX `flock`. Operators must stop the daemon (`docker compose stop election-bot`) before running `whatsapp pair` or `whatsapp logout`.
- **Fail-Closed Device Store**: If `whatsapp-session.db` contains more than one device record, daemon startup fails closed to prevent sending from an ambiguous account.

## Remaining work before production

- Perform an authorized WhatsApp smoke test with a dedicated, non-critical test account and explicit recipient consent.
- Perform a Telegram smoke test with an authorized bot token.
- Obtain and verify the official TSE EA20 presidential runoff contract and EA11 production paths.
- Complete pre-election operational readiness verification prior to the October 25, 2026 election window.

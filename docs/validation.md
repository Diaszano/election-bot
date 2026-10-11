# Verification ledger

Date: 2026-10-10 / 2026-10-11. Implementation branches: `feat/election-bot` (Telegram/core monolith), `feat/whatsapp-integration` (WhatsApp channel extension), and `ci/github-actions-release-pipeline` (CI/CD, SemVer, Docker publishing, and repository standards). Architecture, specifications, and plans were approved before product implementation. Native execution used incremental commits and test-first package/regression checks.

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
| **Formatting & Git whitespace** | `gofmt -l .` returned 0 unformatted files; `git diff --check` reported 0 whitespace errors. | PASS |
| **Operational Docker validation** | Image `election-bot:latest` started with `WHATSAPP_ENABLED=false` and served `/health/ready` (200 OK); started with `WHATSAPP_ENABLED=true` (without session DB) and isolated connection failure (`not_paired`), continuing to serve `/health/ready` and TSE polling without crash. | PASS |
| **Live Grafana & OTel stack verification** | Observability stack started via Docker Compose; verified provisioning of `election-bot-overview` and `election-bot-telemetry` dashboards; verified that queries use actual exported metric names (`whatsapp_connection_state`, `whatsapp_messages_sent_total`, etc.). | PASS |
| **Database migration & backup recovery** | Verified `migrateV2` on populated schema 1; verified row preservation, collision keys (`legacy:<id>`), foreign key integrity (`PRAGMA foreign_key_check`), rollback on injected failure (`TestMigrationFailureRollsBack`), and full restore from pre-migration backup (`TestMigrationBackupRestore`). `migrations/001_initial.sql` remains byte-for-byte identical. | PASS |

## Executed evidence — CI/CD, SemVer, Docker Publishing, and Repository Standards (`ci/github-actions-release-pipeline`)

| Task / Check | Executed verification & evidence | Result |
|---|---|---|
| **Task 1**: Version package & CLI introspection | Implemented `internal/platform/version`, `election-bot version`, and `election-bot version --json`. Injected `version.Get().Version` into OpenTelemetry `service.version` and startup logs. Commit `f53e65a`, `032dd1c`. | PASS |
| **Task 2**: Native commit & branch validator | Implemented zero-dependency Go CLI `cmd/dev-check` for Conventional Commits 1.0.0 (`dev-check commit`), branch naming (`dev-check branch`), and PR titles (`dev-check pr-title`). Covered breaking changes (`!`, footer), pre-1.0 exemptions, and merge commit exemptions in `cmd/dev-check/main_test.go`. Commit `d4cbefe`. | PASS |
| **Task 3**: Release Please configuration & bootstrap | Configured `release-please-config.json` (Go package `election-bot`, `initial-version: "0.1.0"`, `bump-minor-pre-major: true`), `.release-please-manifest.json` (`"0.1.0"`), and `CHANGELOG.md`. Added `dev-check release-config` validator with unit test in `cmd/dev-check/release_test.go`. Commit `bf89e43`. | PASS |
| **Task 4**: GitHub Actions CI workflow | Implemented `.github/workflows/ci.yml` with four mandatory gates: `standards`, `go-quality`, `security` (govulncheck, gitleaks, dependency-review), and `docker-validation` (Buildx, Trivy scanner blocking on `CRITICAL`, non-root smoke test). Verified syntax and permissions in `deployments/docker/ci_workflow_test.go`. Commit `2f17782`. | PASS |
| **Task 5**: Release & publishing workflow | Implemented `.github/workflows/release.yml` with self-contained Release Please (`GITHUB_TOKEN`), exact release commit SHA verification against all mandatory CI checks, multi-arch GHCR publishing (`linux/amd64`, `linux/arm64`), Trivy scan, SPDX SBOM, build provenance attestation, and binary asset cross-compilation. Verified in `deployments/docker/release_workflow_test.go`. Commit `d4e9463`. | PASS |
| **Task 6**: Repository standards & developer tooling | Created `.editorconfig`, `.gitattributes`, `.github/CODEOWNERS`, `.github/dependabot.yml` (weekly gomod/actions/docker with `chore(deps)` prefix), issue and PR templates, `CONTRIBUTING.md`, `SECURITY.md`, and updated `Makefile` (`check`, `ci`, `version`, `release-check`, `git-hooks`). Commit `66fd17f`, `07d80f6`. | PASS |
| **Release Safety**: Pre-tag verification gate | Verified that `verify-release-commit` executes before `release-please` (`needs: verify-release-commit`). All 4 CI gates must pass before Release Please can create any Git tag or GitHub Release. | PASS |
| **Release Safety**: Trivy multi-arch vulnerability gate | Confirmed that both `linux/amd64` (`candidate-amd64`) and `linux/arm64` (`candidate-arm64`) candidate images are built and scanned with `aquasecurity/trivy-action` (`severity: CRITICAL`, `exit-code: '1'`) before `push: true`. Any critical finding on either architecture blocks publication. | PASS |
| **Release Safety**: Initial release bootstrap | Confirmed initial release calculation produces strictly `v0.1.0` via `cmd/dev-check release-config` and `release_test.go`. | PASS |
| **Release Safety**: SHA alignment across artifacts | Verified that Docker images, static binaries, SBOMs, provenance attestations, and OCI labels all resolve and reference the exact same immutable release commit SHA (`TARGET_SHA`). | PASS |
| **Release Safety**: Immutability & recovery security | Verified that manual recovery (`workflow_dispatch` with `recovery_tag`) enforces the same mandatory CI verification and security gates, and cannot overwrite immutable version tags. | PASS |
| **PR Commit Range Validation**: Native Go validator | Implemented `cmd/dev-check pr-commits` and wired into `.github/workflows/ci.yml` `standards` job and `make check`. Covered all 10 regression test cases in `cmd/dev-check/main_test.go`. | PASS |
| **Ruleset Documentation Alignment**: Solo maintainer mode & non-linear history | Corrected ruleset recommendations in `docs/ci-cd.md`: disabled "Require linear history" to support `git merge --no-ff`, and defaulted to Solo Maintainer Mode (0 approvals required) to prevent single-maintainer review deadlock. | PASS |
| **Full test suite (`go test ./...`)** | All 22 packages with test suites passed without errors. | PASS |
| **Race detector (`go test -race ./...`)** | Zero data races detected across all packages. | PASS |
| **Static analysis (`go vet ./...`)** | Clean analysis across entire module. | PASS |
| **Code linting (`make lint`)** | `golangci-lint run` (v2.13.2) reported 0 issues. | PASS |
| **Repository checks (`make check`)** | Branch validation and release-config consistency verified: `ci/github-actions-release-pipeline` valid, manifest matches `0.1.0`. | PASS |
| **Binary compilation (`make build`)** | `go build -trimpath -o bin/election-bot ./cmd/election-bot` built successfully. | PASS |
| **Recovery simulation (`make simulate`)** | Deterministic simulation ran and passed (`election-bot simulation: PASS`). | PASS |
| **Dependency verification (`go mod verify`)** | All dependencies matched pinned module hashes. | PASS |
| **Static build with ldflags (`CGO_ENABLED=0 go build`)** | Pure-Go binary built with `-ldflags` injecting `Version=v0.1.0` and `Commit=testsha`. | PASS |
| **CLI version introspection** | `./bin/election-bot version` returned `election-bot v0.1.0`, `Commit: testsha`, `Go: go1.27.0`, `Platform: linux/amd64`. `./bin/election-bot version --json` returned structured JSON with all fields populated. | PASS |
| **Docker container build** | `docker build -t election-bot:test .` succeeded (15/15 stages finished). | PASS |
| **Docker container smoke test** | Started `election-bot:test` with `DATA_SOURCE_MODE=simulation`; verified HTTP 200 on `/health/ready` and `/health/live`; verified CLI `version` and `version --json` inside container; verified clean SIGTERM shutdown and removal. | PASS |

## Integration boundary and limitations

1. **No Real WhatsApp Account Pairing or Messaging**: No real WhatsApp phone number, credentials, or session keys were used during validation. All WhatsApp adapter behavior was validated against deterministic fake clients, simulated network connections, and offline session stores. whatsmeow is an unofficial WhatsApp Web client. Account restriction or suspension remains an inherent operational risk of automated messaging on WhatsApp, even at low volumes. The official WhatsApp Business Platform is the recommended long-term solution for official production messaging.
2. **No Real Telegram Delivery**: Real public Telegram delivery was not performed; API behavior is tested through authenticated HTTP mock servers.
3. **Official TSE Runoff Contract**: Official mode remains fail-closed by design (`DATA_SOURCE_MODE=official` halts startup). TSE EA20 contract documentation returned HTTP 403, and national runoff winner confirmation fields remain unverified against official production APIs.
4. **Schema Migration Integrity**: `migrations/001_initial.sql` was verified to be completely unmodified. Database migration from v1 to v2 is strictly non-destructive.
5. **Privacy and Secret Scanning**: Scanned all tracked files, commit history, and staging. Zero tokens, passwords, real recipient phone numbers, private JIDs, QR codes, or encryption keys are present in repository files. Tracked `.env.example` contains only placeholder values.
6. **No Remote GitHub Push or Live Actions Execution**: In accordance with the project safety boundary, no commits were pushed to remote GitHub, and no live GitHub Actions workflows were triggered on GitHub servers. All workflow files were validated locally using structural, syntax, and permission tests.
7. **No Remote GHCR Container Publishing**: No container images were pushed to remote `ghcr.io/diaszano/election-bot`. Container builds, multi-platform compatibility, and smoke tests were executed locally.
8. **No Remote Release or Tag Mutation**: No Git tags were pushed and no actual GitHub Releases were published. Release Please configuration was validated via dry-run and unit tests.

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

# WhatsApp Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Add optional, authorized WhatsApp subscriptions and durable delivery to election-bot while preserving Telegram behavior and election safety.

**Architecture:** One shared polling/snapshot pipeline commits channel-aware subscription work into SQLite. Independent Telegram and WhatsApp delivery workers execute application-owned policies without holding transactions during network calls. whatsmeow uses a separate protected device database and a cross-process exclusive session lock.

**Tech Stack:** Go 1.27.0, existing modernc.org/sqlite v1.60.1, pinned whatsmeow `v0.0.0-20261007111105-c386243a72ba`, proposed qrterminal/v3 v3.2.1, existing OpenTelemetry/slog and Docker Compose stack.

**Spec:** [Approved specification with final refinements](../specs/2026-10-10-whatsapp-integration-design.md).

**Status:** Completed and verified across Tasks 1–14 on branch `feat/whatsapp-integration`. All test suites, race detection, linters, builds, simulations, and Docker Compose configurations have passed. See [verification ledger](../../validation.md) for full execution evidence and recorded operational boundaries.

## Global Constraints

- Project, binary, image, and service: `election-bot`; module: `github.com/Diaszano/election-bot`.
- Code, comments, technical logs, and documentation are English; Telegram/WhatsApp user-facing messages are Brazilian Portuguese.
- One modular monolith, one application replica, one TSE worker, independent Telegram/WhatsApp delivery workers; no broker or extra service.
- Preserve existing Telegram behavior, tests, private subscriptions, conditional validators, snapshot precedence, and synchronization/freshness/publication-delay status.
- Do not modify `migrations/001_initial.sql`; preserve Telegram subscriptions, message IDs, generations, pending jobs and history during migration.
- WhatsApp is disabled by default. Only verified private PN/LID identities on the explicit allowlist may opt in; no contact harvesting or automatic cross-channel subscriptions.
- Maximum WhatsApp edit age: 14 minutes from original send. Default edit minimum: 60s; digest: 15m; recipient spacing: 3s; global spacing: 1s.
- Persist immutable WhatsApp send/edit attempt IDs before network calls; identical retries reuse IDs. Guarantees remain at-least-once, not exactly-once.
- Inspect all stored devices before selection; zero permits new pairing, one restores, more than one fails closed.
- Persistent separate session database, owner-only permissions, exclusive cross-process lock shared by runtime/pair/logout; no credentials or private identifiers in telemetry.
- No domain/application SDK imports; constructor injection, context cancellation, parameterized SQL, bounded retries, and no network calls under SQL transactions.
- Preserve the pure-Go/non-root runtime. Verify dependency compatibility and `CGO_ENABLED=0` rather than assuming it.
- Keep official runoff mode fail-closed until production EA20 paths/schema and national finalization/winner semantics are verified.
- Commit incrementally on `feat/whatsapp-integration`; do not push, merge, deploy, or delete branches.

## Review Focus

These boundary cases need explicit tests beyond the ordinary happy path:

1. Schema-valid atypical main rows collide after key normalization: Task 2 must retain every pending/history row with deterministic reserved keys.
2. A main job changes while an ambiguous send is being retried: Tasks 3/5 must preserve the old immutable attempt and avoid reusing its request ID for changed content.
3. A successful edit already delivered the desired presentation before the edit window expires: Tasks 1/10 must avoid an unnecessary digest.
4. Multiple stored devices or path aliases can select/lock the wrong account: Task 6 must reject ambiguous stores and prove cross-process exclusion across aliases.
5. Authorization or generation changes while a send is in flight: Tasks 4/5/9 must prevent obsolete completion/failure from reactivating or deactivating a different lifecycle; document the unavoidable in-flight remote boundary.

---

## File map and contracts

Extend the existing files rather than adding a second election application. New files below separate identity, migration, delivery policy, session management, and commands because each has a different trust boundary.

| Responsibility | Files |
| --- | --- |
| Identity and policy | `internal/domain/subscription/identity.go`, existing `subscription.go`; `internal/domain/notification/policy.go`, existing `delivery.go` |
| Migration and repositories | `migrations/002_channel_notifications.sql`, existing `migrations/embed.go`; `internal/adapters/outbound/sqlite/migrate.go`, `deliveries.go`, `commands.go`, existing `store.go` |
| Shared use cases and publishing | Existing `internal/application/ports/state.go`, `usecases/service.go`; new `usecases/commands.go`, `usecases/publisher.go` |
| Rendering | Existing `internal/application/services/format.go`; new `format_whatsapp.go` |
| WhatsApp infrastructure | `internal/adapters/outbound/whatsapp/{session,lock,client,connection,pair}.go`; `internal/adapters/inbound/whatsapp/commands.go` |
| Composition/configuration | Existing `cmd/election-bot/main.go`, `internal/platform/config/config.go`, `internal/platform/lifecycle/{run,simulation}.go`; new `lifecycle/whatsapp.go` |
| Telemetry/operations | Existing observability files, dashboards, Compose, Dockerfile, Makefile, environment example and documentation |

### Types shared between tasks

Task 1 defines these owned application/domain types; their names are fixed for later tasks:

- `subscription.Channel` string constants `Telegram`, `WhatsApp`.
- `subscription.Identity{Election election.Identity, Channel Channel, Recipient string}`; `NewIdentity(election.Identity, Channel, string) (Identity, error)`; `Identity.Key() string` produces a deterministic full identity key without exposing it to telemetry.
- `subscription.Subscription` gains `ID int64` and `Identity Identity`; generation, active, baseline and preferences retain their meaning; add `NextOutboundAt time.Time` for durable per-recipient pacing.
- `notification.Operation` constants `Send`, `Edit`; `MessageRef{ID string, OriginalSentAt time.Time}`.
- `notification.MainState{Ref MessageRef, LastEditAt, LastSummaryAt, LastActivityAt time.Time, AcceptedSemanticRevision, AcceptedPresentationRevision string, EditUnavailable bool}`.
- `notification.Policy{EditMin, Digest, RecipientMin, GlobalMin time.Duration}`; `MainDecision{Operation Operation, Due time.Time, AlreadyAccepted bool}`. An empty operation means defer/no network call; `AlreadyAccepted` requires a matching prior accepted presentation, not unsent work.
- `notification.DecideMain(now time.Time, state MainState, semanticRevision, presentationRevision string, policy Policy) MainDecision`; constant `MaxEditAge = 14*time.Minute`.
- `notification.Delivery` gains subscriber identity/ID, event key, presentation and semantic revisions, main state, and durable scheduling fields. Keep `Revision` as the presentation revision to avoid competing revision names. Remove numeric transport assumptions after all callers migrate.
- `notification.Attempt{ID int64, DeliveryID, Generation int64, Operation Operation, RequestID, Revision, Text, TargetMessageID string}` holds an immutable transport payload/target. `notification.Acceptance{Ref MessageRef, Operation Operation, AcceptedAt time.Time}` is API acceptance, not a receipt.
- `notification.ChannelState{GlobalNextSendAt, PausedUntil time.Time, AuthenticationRequired bool, AccountBinding, PauseReason string}` stores finite pause classifications and private account binding; it is never exported wholesale.
- `notification.Command{Identity subscription.Identity, IncomingID, Verb string, ReceivedAt time.Time}` contains normalized recognized commands only.

Task 3 changes the repository ports and all affected callers in one buildable commit:

- `Transaction.Subscription(subscription.Identity) (subscription.Subscription, error)`, `PutSubscription(subscription.Subscription) error`, `Cancel(subscription.Identity) error`; snapshot/status methods remain unchanged.
- `ChannelDeliveries.PendingChannel(ctx context.Context, channel subscription.Channel, now time.Time, limit int) ([]notification.Delivery, error)`; retain the numeric, Telegram-only `Deliveries.Pending` adapter until the existing polling publisher is replaced in Task 5.
- `PrepareAttempt(ctx context.Context, d notification.Delivery, op notification.Operation, proposedID string) (notification.Attempt, error)` atomically rechecks generation/revision and persists or restores the immutable attempt.
- `CompleteAttempt(ctx context.Context, d notification.Delivery, a notification.Attempt, accepted notification.Acceptance) error` applies guarded acknowledgement and message state.
- `RescheduleChannel(ctx context.Context, d notification.Delivery, due time.Time, kind string) error`, `DeferDelivery(ctx context.Context, d notification.Delivery, due time.Time) error`, and `MarkDeliveryUndeliverable(ctx context.Context, d notification.Delivery, kind string) error` are generation guarded; deferral does not increment attempts.
- `DisableEditing(ctx context.Context, d notification.Delivery, due time.Time, kind string) error` generation/reference-guards the edit-unavailable flag and schedules a pending summary fallback without discarding work.
- `ResolveAlreadyAccepted(ctx context.Context, d notification.Delivery) error` completes only if current-generation accepted presentation matches; no network counter increment or invented acceptance.
- `CommandStore.ProcessCommand(ctx context.Context, command notification.Command, apply func(Transaction) error) (bool, error)` runs dedup insert and callback in the same transaction; false means duplicate.
- `ports.ChannelSender.Send(ctx context.Context, recipient string, attempt notification.Attempt) (notification.Acceptance, error)` and `Edit(ctx context.Context, recipient string, ref notification.MessageRef, attempt notification.Attempt) (notification.Acceptance, error)`.
- `ports.ChannelStateStore.ReadChannelState(ctx context.Context, channel subscription.Channel) (notification.ChannelState,error)` and `SaveChannelState(ctx context.Context, channel subscription.Channel, state notification.ChannelState) error` persist channel cooldown/authentication/restriction state. Repository acknowledgement updates recipient/global cooldowns in its transaction so caller-written state cannot race acceptance.
- Keep `ports.NotificationSender` unchanged for the Telegram HTTP client/command adapter; implement its channel bridge in Task 5.

Task 5 defines `NewPublisher(state ports.State, deliveries ports.ChannelDeliveries, sender ports.ChannelSender, clock ports.Clock, channel subscription.Channel, policy notification.Policy, available func() bool) *Publisher` and `Publisher.Publish(ctx context.Context) error`. One instance per channel provides independent serialization. Generate proposed request IDs with `crypto/rand` before preparation; preparation persists them before sending. No SDK-specific ID generator enters the application layer.

Task 6 defines `whatsapp.OpenSession(ctx context.Context, path string) (*Session, error)`, `Session.Close() error`, and `AcquireSessionLock(path string) (*SessionLock, error)`. SDK types remain internal to the adapter.

Task 7 defines `whatsapp.NewClient(session *Session, options Options, channelState ports.ChannelStateStore, logger *slog.Logger) *Client`, `Client.Run(ctx context.Context) error`, `Connected() bool`, `State() ConnectionState`, `Close() error`, and channel sender methods above. `Options` carries connect/send timeouts and reconnect enablement; adapter-owned `ConnectionState` has the specification's seven bounded states.

Task 8 defines `whatsapp.Pair(ctx context.Context, path string, terminal io.Writer, timeout time.Duration) error` and `Logout(ctx context.Context, path string, timeout time.Duration) error`. Pair validates that its supplied terminal is an interactive terminal before exposing a QR.

Task 9 defines adapter-neutral `InboundMessage{Chat, Sender, SenderAlt, ID, Text string; FromMe, Group, Newsletter, Historical, Edit bool}` inside the WhatsApp inbound package, never the domain. `IdentityResolver.Resolve(ctx context.Context, chat, sender, senderAlt string) (string, error)` returns only a verified canonical PN. The WhatsApp SDK event translator and resolver implement this boundary.

---

### Task 1: Channel identities, presentation rendering, and delivery decisions

**Files:** Create `internal/domain/subscription/identity.go`, `identity_test.go`; create `internal/domain/notification/policy.go`, `policy_test.go`; modify both existing domain structs; create `internal/application/services/format_whatsapp.go`, `format_whatsapp_test.go`.

**Interfaces:** Produces Task 1 types above. Consumes existing `election.Identity`, `Snapshot.Fingerprint`, formatting helpers, and injectable times; no infrastructure imports.

- [x] **Step 1 — Write failing tests.** Add `TestChannelIdentityIsolation`, `TestWhatsAppMainPolicy`, `TestEditAcceptedDigestDedup`, `TestOriginalSendAnchorAndClockRollback`, and `TestWhatsAppFormatting`. Table assertions include:

```go
// Use fixed UTC times and the existing fictional snapshot helper.
if notification.MaxEditAge != 14*time.Minute { t.Fatal("wrong edit cap") }
// At age 14m: no Edit; at age 13m59s with edit spacing met: Edit.
// AcceptedPresentationRevision == desired: AlreadyAccepted, no Send.
// Last edit does not change OriginalSentAt; clock rollback cannot extend it.
// Freshness-only changes cannot cause a digest; simulation label is exact.
```

- [x] **Step 2 — Confirm red.** Run `go test ./internal/domain/... ./internal/application/services/... -run 'Test(ChannelIdentity|WhatsApp|EditAccepted|OriginalSend)' -count=1`; expect failures for the missing new behavior, not unrelated baseline failures.
- [x] **Step 3 — Implement the types, `NewIdentity`, `Identity.Key`, `DecideMain`, and `FormatWhatsAppResults(snapshot election.Snapshot, location *time.Location, freshness Freshness) string`.** Also add `FormatWhatsAppMilestone(snapshot election.Snapshot, milestone int) string` using existing milestone wording without Telegram HTML. Use existing number/progress/status rules. Hash exact rendered presentation separately from semantic election fingerprint; include latest milestone and source/simulation/freshness labels. Keep legacy fields temporarily so existing callers compile until Task 3.
- [x] **Step 4 — Confirm green.** Run the focused command and `go test ./internal/domain/... ./internal/application/services/...`; preserve existing formatter assertions and no winner inference.
- [x] **Step 5 — Commit.** `git commit -m "feat: model channel identities and WhatsApp presentation policy"` after staging only this task's files.

### Task 2: Lossless schema migration and collision preflight

**Files:** Create `migrations/002_channel_notifications.sql`, `internal/adapters/outbound/sqlite/migrate.go`, `migration_test.go`; modify `migrations/embed.go`; create `test/fixtures/sqlite/legacy-v1.sql`.

**Interfaces:** Consumes Task 1 identity encoding. Produces package-private `migrateV2(ctx context.Context, db *sql.DB, election election.Identity) error` and `legacyEventKeys(rows []legacyRow) (map[int64]string, error)` for repository activation in Task 3. `legacyRow` contains original ID/chat/kind/milestone. Keep normal `Open` on version 1 until repositories can read version 2.

- [x] **Step 1 — Write failing `TestMigrationV1PreservesTelegram`, `TestMigrationCollisionKeys`, `TestLegacyPendingPresentationBarrier`, `TestMigrationFailureRollsBack`, and `TestMigrationBackupRestore`.** Build databases from the unchanged migration 001 and populated fixtures. Compare sorted full rows/metadata before and after, including large integers, canceled/failed/pending history, retry counts, validators, generations, offset and precise due values. Include an accepted newer presentation with an older pending main, stale/equal-time tie cases, exact revision repeats and retry-ambiguous rows; assert obsolete rows remain queryable with original state/reason retained and cannot be selected for send.

```go
// Colliding main rows: (id=11,milestone=0,pending), (12,25,delivered), (13,50,pending).
// Expected event keys: 11 -> main, 12 -> legacy:12, 13 -> legacy:13.
// Without milestone zero, smallest original ID owns main.
// Pending row count, every original ID and every state must be preserved.
// Additionally, accepted (due=300,id=20) supersedes pending (due=200,id=21).
// attempts>0 + a different accepted revision becomes review_required, never deleted.
```

- [x] **Step 2 — Confirm red.** Run `go test ./internal/adapters/outbound/sqlite -run TestMigration -count=1`; prove a naive collapse would violate uniqueness and failure injection exposes partial migrations without rollback.
- [x] **Step 3 — Implement one transactional migration.** Add version-2 subscriptions/outbox, immutable `delivery_attempts`, inbound command dedup, consent audit and channel state. Reserve `legacy:` keys; preserve all rows and AUTOINCREMENT continuity. Apply canonical milestone-zero/smallest-ID selection and reserved keys for additional collisions. Derive accepted presentation precedence by `(due,id)`, never by revision-string ordering. Mark only provably stale or already accepted pending main jobs `superseded`, retaining original state in `legacy_state` and reason separately. Because retry-updated due values lose enqueue chronology, quarantine conflicting attempted jobs as `review_required`; never replay those payloads automatically. Preserve later/no-baseline pending jobs. Check original constraints, row equivalence, uniqueness, `foreign_key_check` and integrity before version advancement. Make fault injection a package-private test seam, not production configuration. Do not edit migration 001 or disable foreign keys to conceal errors.
- [x] **Step 4 — Confirm green and rollback.** Run all SQLite tests. Inject failures after DDL and mid-copy; reopen using the actual previous repository/application version. Test successful reopen, unknown future version rejection, stopped/checkpointed consistent backup integrity/FKs/row counts/notification history, and restoration through the previous binary. Before the first schema-v2 deployment, block rollout until this backup and restore rehearsal is recorded. Verify `git diff -- migrations/001_initial.sql` is empty.
- [x] **Step 5 — Commit.** `git commit -m "feat: add lossless channel migration with rollback coverage"`.

### Task 3: Channel-aware SQLite repositories and immutable attempts

**Files:** Modify `internal/application/ports/state.go`, `internal/adapters/outbound/sqlite/store.go`, `store_test.go`, `internal/application/usecases/service.go`, `service_test.go`, `internal/platform/lifecycle/{run,simulation}.go` and domain structs; create SQLite `deliveries.go`, `commands.go`, `deliveries_test.go`, `commands_test.go`.

**Interfaces:** Produces Task 3 ports. Activates migration through `OpenForElection(ctx context.Context, path string, election election.Identity) (*Store, error)`; retain `Open(ctx,path)` as the existing default-profile wrapper for callers/tests. Legacy `SubscribeUser(ctx,chat)` and `UnsubscribeUser(ctx,chat)` still construct Telegram identities.

- [x] **Step 1 — Write failing `TestPendingChannelIsolation`, `TestAttemptPersistedBeforeSend`, `TestAttemptRetryIdentity`, `TestCoalescedRevisionRetainsAttempt`, `TestLateGenerationOutcomes`, and `TestCommandTransactionDedup`.** Verify identical retries/restarts return the stored ID; changed payload/operation/target/generation creates a new immutable attempt and retains the old record. Migration reserved-key pending rows are independently selectable; unknown legacy kinds remain visible without automatic sends.
- [x] **Step 2 — Confirm red.** Run `go test ./internal/adapters/outbound/sqlite -run 'Test(PendingChannel|Attempt|Coalesced|LateGeneration|CommandTransaction)' -count=1`.
- [x] **Step 3 — Implement repository ports and adapt existing callers atomically.** Activate the runner, replace numeric identity queries, preserve parameterized SQL/WAL/pool settings, preserve Telegram update offsets and legacy main IDs. Enqueue/coalesce only canonical main rows; never overwrite imported reserved rows. Prepare attempts in a short guarded transaction; acknowledge exact revision while preserving a valid remote reference if a newer revision is pending. Generation-guard completion/reschedule/terminal actions. Separate deferral from retry increments; keep failed work durable. Implement `DisableEditing` only for the currently matching main reference, so a late edit failure cannot disable a replacement message. Preserve atomic snapshot/validators/status/outbox transactions. Implement `ChannelStateStore` with constrained updates; acknowledgment/pacing writes cannot overwrite a persisted restriction or authentication pause.
- [x] **Step 4 — Confirm green.** Run `go test ./...`; all existing Telegram, TSE conditional request, opaque ordering, and restart regressions must still pass. Inspect changes to old assertions: change API fixtures only, never weaken behavior to pass the suite.
- [x] **Step 5 — Commit.** `git commit -m "feat: persist channel deliveries and immutable transport attempts"`.

### Task 4: Shared subscriptions and transactional commands

**Files:** Modify `internal/application/usecases/service.go`, `service_test.go`; create `usecases/commands.go`, `commands_test.go`; modify `ports/state.go` only for required audit/reply transaction operations; retain Telegram command adapter behavior.

**Interfaces:** Add `Service.Subscribe(ctx context.Context, identity subscription.Identity) error`, `Unsubscribe(ctx,identity) error`, `ProcessCommand(ctx context.Context, command notification.Command) (bool,error)`, and `ReconcileAllowedRecipients(ctx context.Context, allowed []subscription.Identity) error`. Legacy Telegram use-case methods delegate to shared logic; election settings provide the supported full election identity.

- [x] **Step 1 — Write failing `TestIndependentChannelSubscriptions`, `TestActiveStartKeepsGenerationAndBaseline`, `TestMilestonesAcrossChannelsAndCorrections`, `TestCommandEffectsAtomic`, `TestCommandTransactionNoDeadlock`, and `TestAllowlistRemovalRequiresNewOptIn`.** Prove no historical milestones for a new subscriber; stop/start cannot recreate old ones; duplicate incoming IDs cannot duplicate audit/reply jobs. Use a one-connection SQLite pool and a bounded context: command processing completes atomically through the supplied transaction, with no callback re-entry into `Store`, deadlock, or partial audit/subscription/reply records. Allowlist removal cancels pending work and advances generation, while disabling the transport retains it.
- [x] **Step 2 — Confirm red.** Run `go test ./internal/application/usecases -run 'Test(IndependentChannel|ActiveStart|MilestonesAcross|CommandEffects|Allowlist)' -count=1`.
- [x] **Step 3 — Implement shared use cases.** The transactional command callback operates exclusively through the provided `ports.Transaction`; it must not call `Store`, `State`, or another repository method that can acquire a connection/transaction. Add any necessary audit/reply operation to that interface. Store dedup, normalized consent audit, subscription effects and reply outbox work together. Reuse status calculations for source mode, synchronization freshness and publication delay. Format replies in Portuguese. No outbound network calls occur in commands or snapshots.
- [x] **Step 4 — Confirm green.** Run `go test ./internal/application/... ./internal/adapters/inbound/telegram/... ./internal/adapters/outbound/sqlite/...`; preserve private Telegram `/start` idempotency.
- [x] **Step 5 — Commit.** `git commit -m "feat: share subscription and command rules across channels"`.

### Task 5: Independent publishers and durable send preparation

**Files:** Create `internal/application/usecases/publisher.go`, `publisher_test.go`; create `internal/adapters/outbound/telegram/channel.go`, `channel_test.go`; modify existing service, lifecycle and simulation tests/callers.

**Interfaces:** Produces `Publisher` constructor/method in the contracts section. Telegram channel bridge implements `ports.ChannelSender` using the unchanged numeric Telegram HTTP sender. Add `Store.RequeueFailed(ctx context.Context, channel subscription.Channel) error` for deliberate recovery, guarded by current active generation; use this explicitly rather than automatically resetting exhausted retries.

- [x] **Step 1 — Write failing `TestBlockedWhatsAppDoesNotBlockTelegramOrPoll`, `TestPerRecipientSerialization`, `TestSendRequiresCommittedAttempt`, `TestRetryExhaustionRetainsWork`, `TestNewRevisionAfterAmbiguousAttempt`, and `TestNoTransactionDuringNetwork`.** A fake sender inspects persisted attempts before accepting a call. Bound test waits with contexts; use channels/barriers, not sleeps, to establish concurrency.
- [x] **Step 2 — Confirm red.** Run `go test ./internal/application/usecases ./internal/adapters/outbound/telegram -run 'Test(Blocked|PerRecipient|SendRequires|RetryExhaustion|NewRevision|NoTransaction)' -count=1`.
- [x] **Step 3 — Implement publisher lifecycle.** One mutex per publisher/channel, bounded channel-filtered batches, availability deferral without attempts, durable global/recipient cooldowns and bounded actual-error retries. Prepare immutable request IDs before sending. Successful edits and sends update accepted presentation/semantic revisions. Preserve Telegram message-not-modified, deleted-message fallback, blocked-user and 429 behavior. Only resolve already-accepted work after a guarded revision match. Emit sanitized error classifications.
- [x] **Step 4 — Confirm green/race.** Run `go test -race ./internal/application/... ./internal/adapters/outbound/telegram/... ./internal/platform/lifecycle/...`; cancellation releases worker locks and no fake blocked channel stalls polling.
- [x] **Step 5 — Commit.** `git commit -m "feat: isolate channel publishers with durable retry state"`.

### Task 6: Protected session store, device selection, and cross-process lock

**Files:** Create `internal/adapters/outbound/whatsapp/session.go`, `lock.go`, `session_test.go`, `lock_test.go`; modify `go.mod`, `go.sum`, `.gitignore`, `.dockerignore`.

**Interfaces:** Produces Task 6 session/lock interfaces. A package-private device-selection helper accepts all devices; SDK device pointers never cross into application/domain code.

- [x] **Step 1 — Write failing `TestSelectAllStoredDevices`, `TestSessionSQLitePersistence`, `TestSessionPermissions`, `TestSessionLockAcrossProcesses`, and `TestSessionLockPathAliases`.** Zero/one/multiple devices select new/restore/reject respectively; multiple devices cause neither connection nor QR creation. Helper processes prove lock exclusion and release after exit/crash.
- [x] **Step 2 — Confirm red, then resolve the approved pin.** Record the missing adapter/dependency failure. Install only now, during approved execution: `go get go.mau.fi/whatsmeow@v0.0.0-20261007111105-c386243a72ba`; inspect resolved requirements and checksum changes. Verify `GetAllDevices(ctx)` and SQL container APIs at that pin; stop for review if compatibility requires a material architecture change.
- [x] **Step 3 — Implement session/lock.** Use existing modernc driver through `NewWithDB`, upgrade, explicitly enumerate devices, reject ambiguity, restore exactly one device or create one unpaired device. Acquire a canonical-path OS exclusive lock before SQL open; keep it held until close. Enforce owner-only DB/WAL/SHM/lock permissions without an unsafe process-wide umask race. Sanitize errors and disable upstream protocol logging. Ignore sensitive session/build artifacts in Git/context.
- [x] **Step 4 — Confirm green/compatibility.** Run `go test -race ./internal/adapters/outbound/whatsapp -run 'Test(Select|Session)' -count=1`, `go mod verify`, and `CGO_ENABLED=0 go build ./cmd/election-bot`. Test foreign keys and real store upgrades/reopening without pairing an account.
- [x] **Step 5 — Commit.** `git commit -m "feat: secure persistent WhatsApp sessions and device selection"`.

### Task 7: WhatsApp connection lifecycle and outbound adapter

**Files:** Create `internal/adapters/outbound/whatsapp/client.go`, `connection.go`, `client_test.go`, `connection_test.go`; modify session internals only as required.

**Interfaces:** Produces Task 7 client/connection interfaces and `ports.ChannelSender`; consumes immutable attempts, protected session, injectable clock/event/transport test seams.

- [x] **Step 1 — Write failing `TestConnectionTransitions`, `TestRevocationStopsReconnect`, `TestRestrictionPauseSurvivesRestart`, `TestBoundedReconnectCancellation`, `TestStableRequestIDSent`, `TestSendEditAcceptance`, and `TestSendTimeoutAndCancellation`. Assert a blocked fake send exits on the whole-operation deadline/cancellation rather than relying only on the SDK response timeout.** Assert IDs match committed attempt IDs; edits target the stored original reference; acceptance differs from delivery/read receipts. Capture adversarial raw SDK error text and require safe classifications.
- [x] **Step 2 — Confirm red.** Run `go test ./internal/adapters/outbound/whatsapp -run 'Test(Connection|Revocation|Restriction|Bounded|StableRequest|SendEdit|SendTimeout)' -count=1` using fake transport/events only.
- [x] **Step 3 — Implement client/manager.** Translate pinned SDK messages/events internally; send with persisted request IDs and whole-operation deadlines. Disable competing unbounded library reconnect, own jittered backoff (1s base, 5m cap, three attempts per cycle, 1m cooldown). Respect persistent restriction/authentication pause, stream replacement and outdated-client outcomes. Permanent logout requires reauthentication. Disconnect on shutdown without logging out. Never log raw protocol/error payloads or recipients.
- [x] **Step 4 — Confirm green/race.** Run adapter tests with `-race`; temporary errors retain work, cancellation is prompt, and no infinite invalid-credential loop occurs. No actual account connectivity is part of this check.
- [x] **Step 5 — Commit.** `git commit -m "feat: add bounded WhatsApp connection and send adapter"`.

### Task 8: Interactive pairing/logout and mode-specific configuration

**Files:** Create `internal/adapters/outbound/whatsapp/pair.go`, `pair_test.go`, `internal/platform/lifecycle/whatsapp.go`; modify `cmd/election-bot/main.go`, create `main_test.go`; modify config and config tests, `.env.example`, `go.mod`, `go.sum`.

**Interfaces:** Produces Task 8 Pair/Logout APIs. Add `config.LoadForMode(mode string) (Config,error)` for `runtime`, `pair`, `logout`, `simulation`; keep `Load()` as the runtime wrapper. Add the exact WhatsApp fields/defaults in spec section 9 to `Config`.

- [x] **Step 1 — Write failing `TestPairExistingSession`, `TestPairQRIsTerminalOnly`, `TestPairPersistenceAndCancellation`, `TestLogoutNeedsExclusiveSession`, `TestWhatsAppDisabledConfiguration`, and `TestCLIWhatsAppModes`.** Empty allowlist is invalid for enabled runtime but valid for pairing; no WhatsApp credential/config demand in disabled runtime or offline simulation. Reject ambiguous stores and noninteractive QR output.
- [x] **Step 2 — Confirm red, then install renderer.** Record failures; install `github.com/mdp/qrterminal/v3@v3.2.1` only during approved execution. Verify renderer/API and resolved dependencies, preserving CGO-free builds.
- [x] **Step 3 — Implement CLI/configuration.** Dispatch `whatsapp pair` and `whatsapp logout` before starting normal workers. Obtain QR channel before connection; render only to validated terminal, confirm success after persisted authentication, and release lock/resources on every exit. Logout is explicit, not automated credential deletion. The lifecycle CLI records authentication suspension in the application channel state and clears it only after explicit successful same-account pairing; it never starts polling or delivery workers. Bind account privately; changing the sender account suspends prior consent. Validate strict booleans and positive durations; fixed edit cap is not expandable.
- [x] **Step 4 — Confirm green.** Run `go test ./cmd/election-bot ./internal/platform/config ./internal/adapters/outbound/whatsapp`, `go mod verify`, and a CGO-free build. Tests inject QR events and terminal capability rather than contacting WhatsApp.
- [x] **Step 5 — Commit.** `git commit -m "feat: add secure WhatsApp pairing and logout commands"`.

### Task 9: Verified private WhatsApp commands and consent

**Files:** Create `internal/adapters/inbound/whatsapp/commands.go`, `commands_test.go`; create outbound `identity.go`, `identity_test.go`; modify client event translation and shared command use cases only where required.

**Interfaces:** Produces `IdentityResolver` and `InboundMessage` from the contracts section; `NewHandler(actions *usecases.Service, resolver IdentityResolver, allowed []string, logger *slog.Logger) *Handler`; `Handler.Handle(ctx context.Context, message InboundMessage) error`.

- [x] **Step 1 — Write failing `TestPrivateAllowlistedCommands`, `TestPNLIDCanonicalDedup`, `TestUnresolvedIdentityFailsClosed`, `TestIgnoreOwnGroupNewsletterHistoryEdit`, `TestCommandStableIDDedup`, and `TestOptOutAndAllowlistChangeDuringFlight`.** Same PN/verified LID resolves to one subscription; forged/conflicting alternate IDs cannot authorize. Unrecognized text and unauthorized identities produce no replies. Repeated `!start` preserves baseline/generation; `!parar` stops future unsolicited sends.
- [x] **Step 2 — Confirm red.** Run `go test ./internal/adapters/inbound/whatsapp ./internal/adapters/outbound/whatsapp -run 'Test(Private|PNLID|Unresolved|Ignore|CommandStable|OptOut)' -count=1`.
- [x] **Step 3 — Implement resolver/handler.** Use authenticated store mappings only, validate private sender/chat agreement and allowlist before passing normalized commands to the shared use case. No contact-discovery calls or arbitrary phone derivation. Commit dedup/effects before reporting handler success; perform no outbound network sends inside event callbacks. Document that failed acknowledgement does not guarantee replay.
- [x] **Step 4 — Confirm green.** Run inbound/outbound WhatsApp tests plus Telegram command regressions; assert authorized `!status` includes source, freshness, delay and connection state without exposing account identity.
- [x] **Step 5 — Commit.** `git commit -m "feat: authorize private WhatsApp commands and durable consent"`.

### Task 10: WhatsApp delivery policy and runtime isolation

**Files:** Modify `internal/application/usecases/publisher.go`, `publisher_test.go`, `internal/platform/lifecycle/run.go`, `whatsapp.go`, `run_test.go`, simulation senders; create `test/integration/channels_test.go`.

**Interfaces:** Consumes Tasks 1/3/5/7/9. Runtime wires one publisher per enabled channel, one shared Service, one polling worker, and WhatsApp connection/command handling. No new service or duplicate TSE loop.

- [x] **Step 1 — Write failing `TestEditWindowThenDigest`, `TestDigestAfterAcceptedEditIsSuppressed`, `TestCoalescingKeepsRetryDeadlines`, `TestEditFailureFallbackVisible`, `TestMilestonesBypassDigestNotPacing`, and `TestWhatsAppUnavailablePreservesRuntime`.** Use fake clock and blocking senders to establish exact 60s/14m/15m/3s/1s boundaries; failed edits never renew original-send time. Already edit-accepted content yields no new digest or false send count.
- [x] **Step 2 — Confirm red.** Run `go test ./internal/application/usecases ./internal/platform/lifecycle ./test/integration -run 'Test(EditWindow|DigestAfter|Coalescing|EditFailure|MilestonesBypass|WhatsAppUnavailable)' -count=1`.
- [x] **Step 3 — Integrate policy and runtime.** Persist pacing and accepted revisions after both send/edit. Main coalescing cannot overwrite immutable attempts; repeated edit failures persist edit-unavailable and defer replacement until digest eligibility. Disconnected/unpaired/revoked or locked session states suspend WhatsApp only. Local business readiness remains independent of WhatsApp connectivity. Reconcile allowlist and account binding before automatic work. Wire full configuration and bounded graceful shutdown.
- [x] **Step 4 — Confirm green/race.** Run `go test -race ./internal/application/... ./internal/platform/lifecycle/... ./test/integration/...`; test Telegram/polling progress during a blocked WhatsApp request, cancellation, SQL access during network waits, and restart recovery.
- [x] **Step 5 — Commit.** `git commit -m "feat: apply WhatsApp edit digest and isolated runtime policy"`.

### Task 11: WhatsApp telemetry, privacy checks, and dashboards

**Files:** Modify `internal/platform/observability/{metrics,providers}.go` and tests, existing telemetry helpers and lifecycle state registration; create `whatsapp_metrics.go`, `whatsapp_metrics_test.go`; modify both Grafana dashboard JSON files and `docs/observability.md`.

**Interfaces:** Extend `observability.Values` with bounded WhatsApp state/subscribers/pending/failed counts and latest success; reuse `RegisterState` with enabled flags. Skip registration of WhatsApp-specific instruments when its telemetry flag is false; keep one provider/export pipeline and do not add a second registration framework.

- [x] **Step 1 — Write failing `TestWhatsAppTelemetryPrivacy`, `TestWhatsAppMetricSemantics`, `TestWhatsAppTelemetryDisabled`, and `TestDashboardWhatsAppQueries`.** Capture all three signals in local test receivers, inject sentinel PN/LID/body/QR/key values and unsafe errors, and assert none appear. Assert deferral does not increment failures and API acceptance does not imply delivery/read.
- [x] **Step 2 — Confirm red.** Run `go test ./internal/platform/observability -run 'Test(WhatsAppTelemetry|WhatsAppMetric|Dashboard)' -count=1`.
- [x] **Step 3 — Implement exact spec instruments/spans and dashboards.** Add bounded connection enum and counters/gauges/histogram, failed-work visibility, sanitized lifecycle/retry logs, and context correlation. Preserve resource attributes and all Telegram panels. Define Grafana queries from actual exporter translation; test panel expressions against emitted names/labels. Document disabled/backlog-aware alerts and telemetry-failure isolation.
- [x] **Step 4 — Confirm green/export evidence.** Run observability and adapter tests. With local OTLP receivers, assert trace, metric and log payload reception, not just provider creation. Full Collector-to-backend validation belongs to Task 14 and is explicitly unavailable if infrastructure cannot run.
- [x] **Step 5 — Commit.** `git commit -m "feat: observe WhatsApp delivery without private data"`.

### Task 12: Docker operation and protected persistence

**Files:** Modify `compose.yaml`, `.env.example`, `.dockerignore`, `.gitignore`, `Dockerfile` only as needed, Makefile, README WhatsApp operations; add `deployments/docker/whatsapp_test.go` if command/config checks need a repository test.

**Interfaces:** Preserve application image/service name, non-root user, one persistent data volume and existing public ports. Forward exact config settings and keep disabled defaults. Pair CLI must work with image entrypoint.

- [x] **Step 1 — Write failing operator checks.** Assert Compose retains Telegram settings and adds all WhatsApp settings; session path differs from application DB; image command reaches `whatsapp pair`; forbidden secret/session paths are excluded from build context. Check default disabled operation still needs no WhatsApp account.
- [x] **Step 2 — Run checks red.** Use a Go fixture test or assertion script in the existing Docker test package; execute its focused test and confirm missing forwarding/CLI assumptions fail.
- [x] **Step 3 — Implement minimal config/image/doc changes.** Retain CGO-free builder/non-root runtime and do not add a WhatsApp container or port. Document `docker compose stop election-bot`, `docker compose run --rm -it election-bot whatsapp pair`, and `docker compose up -d election-bot`, plus equivalent exclusive logout, protected backup and permission handling.
- [x] **Step 4 — Verify.** Run `docker compose -f compose.yaml -f compose.observability.yaml config --quiet`, application-only config validation, and `docker build -t election-bot .`. A missing daemon is a recorded unperformed image/runtime check; independent tests/config parsers still run. Do not describe a parser-only check as a successful Compose build.
- [x] **Step 5 — Commit.** `git commit -m "build: support secure WhatsApp operation in the existing container"`.

### Task 13: Deterministic dual-channel restart and failure simulation

**Files:** Modify `internal/platform/lifecycle/simulation.go`, simulation tests; create `test/integration/whatsapp_recovery_test.go`; retain existing `test/integration/{simulation,lifecycle}_test.go` and official TSE fixtures.

**Interfaces:** Existing `lifecycle.Simulate(ctx,path)` remains the credential-free entrypoint; extend recording/fake channel senders, fake device session and event sources with deterministic clocks. No real account required.

- [x] **Step 1 — Write failing `TestDualChannelRecovery`, `TestAmbiguousAcceptanceRestart`, `TestRetryExhaustedWorkRequeue`, and `TestOfficialRunoffGateUnchanged`.** Subscribe separately, accept one simulated snapshot, send both main results/milestones, disconnect WhatsApp, continue Telegram/polling, restart application/store, restore fake session, resume pending work, and replay unchanged/corrected results without historical milestones. Ambiguous acceptance retries retain IDs; changed payload gets another ID while prior history remains.
- [x] **Step 2 — Confirm red.** Run `go test ./test/integration -run 'Test(DualChannel|Ambiguous|RetryExhausted|OfficialRunoff)' -count=1`.
- [x] **Step 3 — Extend the deterministic simulation.** Use synchronization barriers and fake clock advancement instead of timing-dependent sleeps. Reopen the same application DB and supported local device store where applicable. Mark fake authenticated device restoration distinctly from actual WhatsApp authentication. Existing election simulation stays operational with neither Telegram nor WhatsApp credentials.
- [x] **Step 4 — Confirm green.** Run integration tests, `make simulate`, and `go test -race ./test/integration/...`; assert explicit final counts, accepted edits, immutable attempts, milestone history and recovery outcomes.
- [x] **Step 5 — Commit.** `git commit -m "test: simulate dual-channel delivery and durable recovery"`.

### Task 14: Documentation, full verification ledger, and final review

**Files:** Update README, `docs/architecture.md`, `docs/observability.md`, `docs/validation.md`; create `docs/decisions/0008-channel-notifications.md`, `0009-whatsapp-sessions.md`, `0010-whatsapp-delivery-policy.md`; update this plan's checkboxes with executed evidence.

**Interfaces:** No new production API. Document the final implemented behavior, not abandoned intermediate adapters or unexecuted commands.

- [x] **Step 1 — Check spec coverage and write missing docs.** Include diagrams, authorized recipient syntax, commands, source/freshness behavior, pairing/logout/account change, locking, backup/rollback, reconnect, edit/digest limitations, retry exhaustion/requeue, session sensitivity, account restrictions even at low volume, at-least-once duplicates and future official Business Platform alternative.
- [x] **Step 2 — Run the complete verification set.** Record exact command output/status for `go test ./...`, `go test -race ./...`, `go vet ./...`, `make lint`, `make build`, `make simulate`, `go mod verify`, `CGO_ENABLED=0 go build ./cmd/election-bot`, both Compose config forms and image build. Run `gofmt`/repository formatting checks and `git diff --check`. Fix failures with a focused red/green regression before rerunning affected checks.
- [x] **Step 3 — Verify actual infrastructure where available.** Emit deterministic fake WhatsApp operations into the existing OTLP Collector stack; query Prometheus for real samples, Loki for sanitized correlated logs, Tempo for received traces, and Grafana provisioning/dashboard queries. Record sample/query evidence. If Docker/backend access is unavailable, explicitly list each unperformed validation; successful local OTLP receiver tests are narrower evidence. Do not pair/send to a real account without authorized smoke-test setup.
- [x] **Step 4 — Review privacy and safety.** Inspect staged files and history for tokens, real recipient identities, QR/session/key files and credentials with redacted output; verify migration 001 unchanged. Reconfirm official mode rejects unverified runoff configuration. Document real WhatsApp and Telegram delivery as unverified, plus official EA20/national finalization and pre-election production readiness work. Review the entire branch against the spec using the required review skill after execution; resolve substantive findings.
- [x] **Step 5 — Commit final documentation.** `git commit -m "docs: record WhatsApp operation and final validation limits"`. Verify clean working tree and report branch/commits, checks performed/not performed and remaining risks. Do not merge, push or deploy.

## Verification ledger template

The executor maintains this ledger in `docs/validation.md`, not a blanket assertion of success:

| Check | Evidence to record | Required distinction |
| --- | --- | --- |
| TDD per task | Red command/reason, green command/result, commit | Expected regression failure versus broken test setup |
| Migration | Old-schema populated test, collisions, injected rollback, backup restore | No destructive downgrade or lost pending/history rows |
| Concurrency/recovery | Race output and deterministic barriers/restart assertions | WhatsApp delay cannot serialize Telegram/polling |
| Dependency/build | Resolved versions, mod verification, CGO-free build | API compatibility versus actual account interoperability |
| Telemetry | Local receiver payloads and backend query evidence separately | Export reception versus real Collector/Tempo/Loki/Prometheus delivery |
| Docker | Config/parser/image/runtime commands and exact outcomes | Daemon unavailable is not a passed image/runtime check |
| Account smoke tests | Explicit authorization and actual outcomes, if performed | Fakes never validate real pairing, authentication or delivery |
| Election safety | Fail-closed test and known verification gaps | Simulation/100% progress never establishes official winner semantics |

## Self-review and execution handoff

Coverage mapping: spec sections 1–4 → Tasks 1–5/10; identity and commands → Tasks 4/9; migration/recovery → Tasks 2/3/13; sessions → Tasks 6–8; delivery → Tasks 1/5/10; configuration/Docker → Tasks 8/12; observability → Task 11/14; tests/docs/acceptance → Tasks 13/14. All four final refinements have explicit regression tests and commits.

The plan preserves the earlier project's Native execution preference; implementation remains gated on review of this plan. If that preference is changed, select the corresponding required execution skill before starting. No dependencies or production code are installed/written while preparing this document.

package integration

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Diaszano/election-bot/internal/adapters/outbound/simulation"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/sqlite"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/application/usecases"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
	"github.com/Diaszano/election-bot/internal/platform/config"
	_ "modernc.org/sqlite"
)

var recoveryElection = election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}

type recoveryClock struct{ now time.Time }

func (c *recoveryClock) Now() time.Time { return c.now }

type recordedAttempt struct {
	operation                 notification.Operation
	requestID, revision, text string
}

type recoverySender struct {
	mu       sync.Mutex
	clock    *recoveryClock
	failures int
	next     int
	attempts []recordedAttempt
}

func (s *recoverySender) deliver(attempt notification.Attempt, edit bool) (notification.Acceptance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, recordedAttempt{attempt.Operation, attempt.RequestID, attempt.Revision, attempt.Text})
	if s.failures > 0 {
		s.failures--
		return notification.Acceptance{}, &ports.ExternalError{Kind: "transient"}
	}
	s.next++
	ref := notification.MessageRef{ID: string(rune('a' + s.next)), OriginalSentAt: s.clock.Now()}
	if edit {
		ref = notification.MessageRef{ID: attempt.TargetMessageID, OriginalSentAt: s.clock.Now().Add(-time.Minute)}
	}
	return notification.Acceptance{Operation: attempt.Operation, Ref: ref, AcceptedAt: s.clock.Now()}, nil
}
func (s *recoverySender) Send(_ context.Context, _ string, attempt notification.Attempt) (notification.Acceptance, error) {
	return s.deliver(attempt, false)
}
func (s *recoverySender) Edit(_ context.Context, _ string, _ notification.MessageRef, attempt notification.Attempt) (notification.Acceptance, error) {
	return s.deliver(attempt, true)
}
func (s *recoverySender) snapshotAttempts() []recordedAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedAttempt(nil), s.attempts...)
}

func recoveryStore(t *testing.T, ctx context.Context, path string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.OpenForElection(ctx, path, recoveryElection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
func recoveryIdentities() (subscription.Identity, subscription.Identity) {
	tg, _ := subscription.NewIdentity(recoveryElection, subscription.Telegram, "123")
	wa, _ := subscription.NewIdentity(recoveryElection, subscription.WhatsApp, "5511999999999@s.whatsapp.net")
	return tg, wa
}
func recoveryService(store *sqlite.Store, clock *recoveryClock, allowed ...subscription.Identity) *usecases.Service {
	return usecases.New(store, store, simulation.New(), nil, clock, usecases.Settings{Election: recoveryElection, Location: time.UTC, StaleAfter: time.Hour, DelayedAfter: time.Hour, AllowedWhatsAppRecipients: allowed})
}
func mustPending(t *testing.T, store *sqlite.Store, channel subscription.Channel, now time.Time) []notification.Delivery {
	t.Helper()
	jobs, err := store.PendingChannel(context.Background(), channel, now, 100)
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}
func historyDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDualChannelRecovery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "dual.db")
	clock := &recoveryClock{time.Date(2026, 10, 25, 20, 25, 0, 0, time.UTC)}
	tg, wa := recoveryIdentities()
	store := recoveryStore(t, ctx, path)
	svc := recoveryService(store, clock, wa)
	if err := svc.Subscribe(ctx, tg); err != nil {
		t.Fatal(err)
	}
	if err := svc.Subscribe(ctx, wa); err != nil {
		t.Fatal(err)
	}
	snapshot := simulation.Snapshot(25)
	if err := svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &snapshot}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveChannelState(ctx, subscription.WhatsApp, notification.ChannelState{AuthenticationRequired: true, PauseReason: "authentication_required"}); err != nil {
		t.Fatal(err)
	}
	tgSender, waSender := &recoverySender{clock: clock}, &recoverySender{clock: clock}
	tgPublisher := usecases.NewPublisher(subscription.Telegram, store, tgSender, clock, notification.Policy{})
	waPublisher := usecases.NewPublisher(subscription.WhatsApp, store, waSender, clock, notification.Policy{})
	if err := waPublisher.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	if len(waSender.snapshotAttempts()) != 0 {
		t.Fatal("disconnected WhatsApp attempted delivery")
	}
	if len(mustPending(t, store, subscription.WhatsApp, clock.Now().Add(time.Hour))) < 2 {
		t.Fatal("WhatsApp work was lost while disconnected")
	}
	if err := tgPublisher.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	if len(tgSender.snapshotAttempts()) < 2 {
		t.Fatal("Telegram stopped while WhatsApp was unavailable")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = recoveryStore(t, ctx, path)
	clock.now = clock.now.Add(2 * time.Minute)
	if err := store.SaveChannelState(ctx, subscription.WhatsApp, notification.ChannelState{PauseReason: "connected"}); err != nil {
		t.Fatal(err)
	}
	waPublisher = usecases.NewPublisher(subscription.WhatsApp, store, waSender, clock, notification.Policy{})
	if err := waPublisher.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(waSender.snapshotAttempts()); got < 2 {
		t.Fatalf("recovered WhatsApp work not sent: %d", got)
	}
	if jobs := mustPending(t, store, subscription.WhatsApp, clock.Now().Add(time.Hour)); len(jobs) != 0 {
		t.Fatalf("recovered jobs remain pending: %d", len(jobs))
	}
	accepted := len(waSender.snapshotAttempts())
	if err := recoveryService(store, clock, wa).ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &snapshot}); err != nil {
		t.Fatal(err)
	}
	if err := waPublisher.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	if len(waSender.snapshotAttempts()) != accepted {
		t.Fatal("unchanged snapshot replayed historical notification")
	}
	var milestoneHistory int
	if err := historyDB(t, path).QueryRowContext(ctx, `SELECT count(*) FROM outbox o JOIN subscriptions s ON s.id=o.subscription_id WHERE s.channel='whatsapp' AND o.kind='milestone' AND o.milestone=25`).Scan(&milestoneHistory); err != nil {
		t.Fatal(err)
	}
	if milestoneHistory != 1 {
		t.Fatalf("historical 25%% milestone count=%d", milestoneHistory)
	}
}

func TestAmbiguousAcceptanceRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ambiguous.db")
	clock := &recoveryClock{time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC)}
	_, wa := recoveryIdentities()
	store := recoveryStore(t, ctx, path)
	if err := recoveryService(store, clock, wa).Subscribe(ctx, wa); err != nil {
		t.Fatal(err)
	}
	firstSnapshot := simulation.Snapshot(0)
	if err := recoveryService(store, clock, wa).ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &firstSnapshot}); err != nil {
		t.Fatal(err)
	}
	sender := &recoverySender{clock: clock, failures: 1}
	publisher := usecases.NewPublisher(subscription.WhatsApp, store, sender, clock, notification.Policy{})
	if err := publisher.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	first := sender.snapshotAttempts()[0]
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = recoveryStore(t, ctx, path)
	clock.now = clock.now.Add(10 * time.Minute)
	publisher = usecases.NewPublisher(subscription.WhatsApp, store, sender, clock, notification.Policy{})
	if err := publisher.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	attempts := sender.snapshotAttempts()
	if len(attempts) < 2 || attempts[0].requestID != attempts[1].requestID {
		t.Fatalf("ambiguous retry did not reuse stable request ID: %+v", attempts)
	}
	newSnapshot := simulation.Snapshot(25)
	if err := recoveryService(store, clock, wa).ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &newSnapshot}); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	attempts = sender.snapshotAttempts()
	var newMain *recordedAttempt
	for i := range attempts {
		if attempts[i].operation == notification.Edit {
			newMain = &attempts[i]
			break
		}
	}
	if newMain == nil || newMain.requestID == first.requestID || newMain.revision == first.revision {
		t.Fatalf("changed presentation reused immutable attempt: first=%+v attempts=%+v", first, attempts)
	}
	var history int
	if err := historyDB(t, path).QueryRowContext(ctx, `SELECT count(*) FROM delivery_attempts a JOIN outbox o ON o.id=a.outbox_id JOIN subscriptions s ON s.id=o.subscription_id WHERE s.channel='whatsapp' AND o.kind='main'`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if history < 2 {
		t.Fatalf("delivery attempt history was lost: %d", history)
	}
}

func TestRetryExhaustedWorkRequeue(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "requeue.db")
	clock := &recoveryClock{time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC)}
	_, wa := recoveryIdentities()
	store := recoveryStore(t, ctx, path)
	if err := recoveryService(store, clock, wa).Subscribe(ctx, wa); err != nil {
		t.Fatal(err)
	}
	snapshot := simulation.Snapshot(0)
	if err := recoveryService(store, clock, wa).ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &snapshot}); err != nil {
		t.Fatal(err)
	}
	sender := &recoverySender{clock: clock, failures: 10}
	publisher := usecases.NewPublisher(subscription.WhatsApp, store, sender, clock, notification.Policy{}, usecases.WithMaxAttempts(1))
	if err := publisher.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.RequeueFailed(ctx, subscription.WhatsApp); err != nil {
		t.Fatal(err)
	}
	if len(mustPending(t, store, subscription.WhatsApp, clock.Now().Add(time.Hour))) == 0 {
		t.Fatal("exhausted outbox work was not requeued")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = recoveryStore(t, ctx, path)
	if len(mustPending(t, store, subscription.WhatsApp, clock.Now().Add(time.Hour))) == 0 {
		t.Fatal("requeued work did not survive restart")
	}
}

func TestOfficialRunoffGateUnchanged(t *testing.T) {
	t.Setenv("DATA_SOURCE_MODE", "official")
	t.Setenv("TELEGRAM_ENABLED", "false")
	t.Setenv("OTEL_ENABLED", "false")
	_, err := config.LoadForMode("runtime")
	if err == nil || !strings.Contains(err.Error(), "contract unverified") {
		t.Fatalf("official TSE runoff gate opened: %v", err)
	}
}

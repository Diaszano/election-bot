package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
)

var fixtureElection = election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}

const fixtureRecipient = "5511999999999@s.whatsapp.net"

func activate(t *testing.T, ctx context.Context, store *Store, identity subscription.Identity, generation int64) {
	t.Helper()
	err := store.Transaction(ctx, func(tx ports.Transaction) error {
		return tx.PutSubscription(subscription.Subscription{Identity: identity, Active: true, Generation: generation, ResultsEnabled: true, MilestonesEnabled: true})
	})
	if err != nil {
		t.Fatal(err)
	}
}
func newWAIdentity() subscription.Identity {
	id, _ := subscription.NewIdentity(fixtureElection, subscription.WhatsApp, fixtureRecipient)
	return id
}
func newWAStore(t *testing.T, name string) *Store {
	t.Helper()
	s, err := OpenForElection(context.Background(), t.TempDir()+"/"+name, fixtureElection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestPendingChannelIsolation(t *testing.T) {
	ctx := context.Background()
	store := newWAStore(t, "channels.db")
	tg, _ := subscription.NewIdentity(fixtureElection, subscription.Telegram, "123")
	wa := newWAIdentity()
	activate(t, ctx, store, tg, 1)
	activate(t, ctx, store, wa, 1)
	now := time.Now().UTC()
	for _, id := range []subscription.Identity{tg, wa} {
		if err := store.Transaction(ctx, func(tx ports.Transaction) error {
			return tx.Enqueue(notification.Delivery{Identity: id, Generation: 1, Kind: "main", Revision: "r1", SemanticRevision: "s1", Text: string(id.Channel), Due: now})
		}); err != nil {
			t.Fatal(err)
		}
	}
	tgJobs, err := store.PendingChannel(ctx, subscription.Telegram, now.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	waJobs, err := store.PendingChannel(ctx, subscription.WhatsApp, now.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tgJobs) != 1 || tgJobs[0].Identity.Channel != subscription.Telegram {
		t.Fatalf("Telegram jobs: %+v", tgJobs)
	}
	if len(waJobs) != 1 || waJobs[0].Identity.Channel != subscription.WhatsApp || waJobs[0].Identity.Recipient != fixtureRecipient {
		t.Fatalf("WhatsApp jobs: %+v", waJobs)
	}
}

func TestAttemptPersistedBeforeSend(t *testing.T) {
	ctx := context.Background()
	store := newWAStore(t, "attempt.db")
	id := newWAIdentity()
	activate(t, ctx, store, id, 1)
	if err := store.Transaction(ctx, func(tx ports.Transaction) error {
		return tx.Enqueue(notification.Delivery{Identity: id, Generation: 1, Kind: "main", Revision: "r1", Text: "immutable", Due: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.PendingChannel(ctx, subscription.WhatsApp, time.Now().Add(time.Second), 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%v err=%v", jobs, err)
	}
	attempt, err := store.PrepareAttempt(ctx, jobs[0], notification.Send, "stable-id")
	if err != nil {
		t.Fatal(err)
	}
	var persisted string
	if err = store.db.QueryRowContext(ctx, `SELECT request_id FROM delivery_attempts WHERE outbox_id=?`, jobs[0].ID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != "stable-id" || attempt.RequestID != "stable-id" || attempt.Text != "immutable" {
		t.Fatalf("attempt not durably prepared: %+v persisted=%q", attempt, persisted)
	}
}

func TestAttemptRetryIdentity(t *testing.T) {
	ctx := context.Background()
	store := newWAStore(t, "retry.db")
	id := newWAIdentity()
	activate(t, ctx, store, id, 1)
	if err := store.Transaction(ctx, func(tx ports.Transaction) error {
		return tx.Enqueue(notification.Delivery{Identity: id, Generation: 1, Kind: "main", Revision: "r1", Text: "same", Due: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.PendingChannel(ctx, subscription.WhatsApp, time.Now().Add(time.Second), 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%v err=%v", jobs, err)
	}
	first, err := store.PrepareAttempt(ctx, jobs[0], notification.Send, "request-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.PrepareAttempt(ctx, jobs[0], notification.Send, "request-two")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.RequestID != "request-one" || second.RequestID != "request-one" {
		t.Fatalf("retry changed request ID: first=%+v second=%+v", first, second)
	}
}

func TestChannelStatisticsOnlyCountSelectedChannel(t *testing.T) {
	ctx := context.Background()
	store := newWAStore(t, "channel-statistics.db")
	wa := newWAIdentity()
	tg, _ := subscription.NewIdentity(fixtureElection, subscription.Telegram, "123")
	activate(t, ctx, store, wa, 1)
	activate(t, ctx, store, tg, 1)
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.Transaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Enqueue(notification.Delivery{Identity: wa, Generation: 1, Kind: "main", Revision: "r1", Text: "summary", Due: now}); err != nil {
			return err
		}
		if err := tx.Enqueue(notification.Delivery{Identity: tg, Generation: 1, Kind: "main", Revision: "t1", Text: "summary", Due: now}); err != nil {
			return err
		}
		return tx.Enqueue(notification.Delivery{Identity: wa, Generation: 1, Kind: "milestone", Milestone: 25, Revision: "m25", Text: "milestone", Due: now})
	}); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.PendingChannel(ctx, subscription.WhatsApp, now.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	var main notification.Delivery
	for _, job := range jobs {
		if job.Kind == "main" {
			main = job
			break
		}
	}
	attempt, err := store.PrepareAttempt(ctx, main, notification.Send, "request-statistics")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteAttempt(ctx, main, attempt, notification.Acceptance{Ref: notification.MessageRef{ID: "remote", OriginalSentAt: now}, Operation: notification.Send, AcceptedAt: now}); err != nil {
		t.Fatal(err)
	}
	subscribers, pending, failed, last, err := store.ChannelStatistics(ctx, subscription.WhatsApp)
	if err != nil || subscribers != 1 || pending != 1 || failed != 0 || !last.Equal(now) {
		t.Fatalf("WhatsApp stats subscribers=%d pending=%d failed=%d last=%s err=%v", subscribers, pending, failed, last, err)
	}
	for _, job := range jobs {
		if job.Kind == "milestone" {
			if err = store.MarkDeliveryUndeliverable(ctx, job, "blocked"); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, pending, failed, _, err = store.ChannelStatistics(ctx, subscription.WhatsApp)
	if err != nil || pending != 0 || failed != 1 {
		t.Fatalf("failed work stats pending=%d failed=%d err=%v", pending, failed, err)
	}
}

func TestCoalescedRevisionRetainsAttempt(t *testing.T) {
	ctx := context.Background()
	store := newWAStore(t, "coalesce.db")
	id := newWAIdentity()
	activate(t, ctx, store, id, 1)
	enqueue := func(rev, text string) error {
		return store.Transaction(ctx, func(tx ports.Transaction) error {
			return tx.Enqueue(notification.Delivery{Identity: id, Generation: 1, Kind: "main", EventKey: "main:1", Revision: rev, Text: text, Due: time.Now()})
		})
	}
	if err := enqueue("r1", "r1"); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.PendingChannel(ctx, subscription.WhatsApp, time.Now().Add(time.Second), 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%v err=%v", jobs, err)
	}
	prior, err := store.PrepareAttempt(ctx, jobs[0], notification.Send, "immutable-r1")
	if err != nil {
		t.Fatal(err)
	}
	if err = enqueue("r2", "r2"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = store.db.QueryRowContext(ctx, `SELECT count(*) FROM outbox WHERE kind='main'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("coalesced rows=%d err=%v", count, err)
	}
	var oldRevision, oldText, oldRequest string
	if err = store.db.QueryRowContext(ctx, `SELECT revision,text,request_id FROM delivery_attempts WHERE id=?`, prior.ID).Scan(&oldRevision, &oldText, &oldRequest); err != nil {
		t.Fatal(err)
	}
	if oldRevision != "r1" || oldText != "r1" || oldRequest != "immutable-r1" {
		t.Fatalf("prior immutable attempt changed: %s %s %s", oldRevision, oldText, oldRequest)
	}
	jobs, err = store.PendingChannel(ctx, subscription.WhatsApp, time.Now().Add(time.Second), 10)
	if err != nil || len(jobs) != 1 || jobs[0].Revision != "r2" {
		t.Fatalf("stale coalesced presentation eligible: %+v err=%v", jobs, err)
	}
}

func TestLateGenerationOutcomes(t *testing.T) {
	ctx := context.Background()
	store := newWAStore(t, "generation.db")
	id := newWAIdentity()
	activate(t, ctx, store, id, 2)
	old := notification.Delivery{Identity: id, Generation: 1, Kind: "milestone", EventKey: "milestone:25", Revision: "m25", Due: time.Now()}
	if err := store.Transaction(ctx, func(tx ports.Transaction) error { return tx.Enqueue(old) }); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeliveryUndeliverable(ctx, old, "invalid_recipient"); err != nil {
		t.Fatal(err)
	}
	var active, generation int
	if err := store.db.QueryRowContext(ctx, `SELECT active,generation FROM subscriptions`).Scan(&active, &generation); err != nil || active != 1 || generation != 2 {
		t.Fatalf("late failure changed generation: active=%d generation=%d err=%v", active, generation, err)
	}
}

package usecases

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Diaszano/election-bot/internal/adapters/outbound/simulation"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/sqlite"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
)

var testElection = election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}

func testService(t *testing.T) (*Service, *sqlite.Store, *clock) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "commands.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := &clock{time.Now()}
	return New(db, db, simulation.New(), &sender{}, c, Settings{Election: testElection, Location: time.UTC, StaleAfter: time.Hour, DelayedAfter: time.Hour}), db, c
}

func identities(t *testing.T) (subscription.Identity, subscription.Identity) {
	t.Helper()
	tg, err := subscription.NewIdentity(testElection, subscription.Telegram, "123")
	if err != nil {
		t.Fatal(err)
	}
	wa, err := subscription.NewIdentity(testElection, subscription.WhatsApp, "5511999999999@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	return tg, wa
}

func countKind(jobs []notification.Delivery, kind string) int {
	n := 0
	for _, job := range jobs {
		if job.Kind == kind {
			n++
		}
	}
	return n
}

func TestIndependentChannelSubscriptions(t *testing.T) {
	svc, db, c := testService(t)
	tg, wa := identities(t)
	if err := svc.Subscribe(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	if err := svc.Subscribe(context.Background(), wa); err != nil {
		t.Fatal(err)
	}
	for _, id := range []subscription.Identity{tg, wa} {
		var got subscription.Subscription
		if err := db.Transaction(context.Background(), func(tx ports.Transaction) error { var e error; got, e = tx.SubscriptionByIdentity(id); return e }); err != nil {
			t.Fatal(err)
		}
		if !got.Active || got.Generation != 1 {
			t.Fatalf("subscription not independent: %+v", got)
		}
	}
	_ = c
}

func TestActiveStartKeepsGenerationAndBaseline(t *testing.T) {
	svc, db, _ := testService(t)
	_, wa := identities(t)
	snap := simulation.Snapshot(50)
	if err := svc.ProcessElectionSnapshot(context.Background(), ports.FetchResult{Snapshot: &snap}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Subscribe(context.Background(), wa); err != nil {
		t.Fatal(err)
	}
	var before subscription.Subscription
	if err := db.Transaction(context.Background(), func(tx ports.Transaction) error { var e error; before, e = tx.SubscriptionByIdentity(wa); return e }); err != nil {
		t.Fatal(err)
	}
	snap = simulation.Snapshot(75)
	if err := svc.ProcessElectionSnapshot(context.Background(), ports.FetchResult{Snapshot: &snap}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Subscribe(context.Background(), wa); err != nil {
		t.Fatal(err)
	}
	var after subscription.Subscription
	if err := db.Transaction(context.Background(), func(tx ports.Transaction) error { var e error; after, e = tx.SubscriptionByIdentity(wa); return e }); err != nil {
		t.Fatal(err)
	}
	if before.Generation != after.Generation || before.Baseline != after.Baseline {
		t.Fatalf("repeated start reset subscription: before=%+v after=%+v", before, after)
	}
}

func TestMilestonesAcrossChannelsAndCorrections(t *testing.T) {
	svc, db, _ := testService(t)
	tg, wa := identities(t)
	snap := simulation.Snapshot(50)
	if err := svc.ProcessElectionSnapshot(context.Background(), ports.FetchResult{Snapshot: &snap}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Subscribe(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	if err := svc.Subscribe(context.Background(), wa); err != nil {
		t.Fatal(err)
	}
	// New subscribers baseline at the accepted snapshot and receive no historical milestones.
	var milestones int
	jobs, err := db.PendingChannel(context.Background(), subscription.Telegram, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	milestones = countKind(jobs, "milestone")
	waJobs, err := db.PendingChannel(context.Background(), subscription.WhatsApp, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	milestones += countKind(waJobs, "milestone")
	if milestones != 0 {
		t.Fatalf("historical milestones queued: %d", milestones)
	}
	snap = simulation.Snapshot(75)
	if err := svc.ProcessElectionSnapshot(context.Background(), ports.FetchResult{Snapshot: &snap}); err != nil {
		t.Fatal(err)
	}
	jobs, err = db.PendingChannel(context.Background(), subscription.Telegram, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	milestones = countKind(jobs, "milestone")
	waJobs, err = db.PendingChannel(context.Background(), subscription.WhatsApp, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	milestones += countKind(waJobs, "milestone")
	if milestones != 2 {
		t.Fatalf("expected one 75%% milestone per channel, got %d", milestones)
	}
	correction := simulation.Snapshot(50)
	correction.ID = "corrected-generation"
	correction.Published = snap.Published.Add(time.Second)
	correction.Generated = snap.Generated.Add(time.Second)
	if err := svc.ProcessElectionSnapshot(context.Background(), ports.FetchResult{Snapshot: &correction}); err != nil {
		t.Fatal(err)
	}
	reachedAgain := simulation.Snapshot(75)
	reachedAgain.ID = "reached-again"
	reachedAgain.Published = correction.Published.Add(time.Second)
	reachedAgain.Generated = correction.Generated.Add(time.Second)
	if err := svc.ProcessElectionSnapshot(context.Background(), ports.FetchResult{Snapshot: &reachedAgain}); err != nil {
		t.Fatal(err)
	}
	jobs, err = db.PendingChannel(context.Background(), subscription.Telegram, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	milestones = countKind(jobs, "milestone")
	waJobs, err = db.PendingChannel(context.Background(), subscription.WhatsApp, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	milestones += countKind(waJobs, "milestone")
	if milestones != 2 {
		t.Fatalf("correction recreated milestone history: %d", milestones)
	}
}

func TestCommandEffectsAtomic(t *testing.T) {
	svc, db, _ := testService(t)
	_, wa := identities(t)
	cmd := notification.Command{Identity: wa, IncomingID: "inbound-1", Verb: "start", ReceivedAt: time.Now()}
	processed, err := svc.ProcessCommand(context.Background(), cmd)
	if err != nil || !processed {
		t.Fatalf("processed=%t err=%v", processed, err)
	}
	processed, err = svc.ProcessCommand(context.Background(), cmd)
	if err != nil || processed {
		t.Fatalf("duplicate processed=%t err=%v", processed, err)
	}
	var got subscription.Subscription
	if err = db.Transaction(context.Background(), func(tx ports.Transaction) error { var e error; got, e = tx.SubscriptionByIdentity(wa); return e }); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.PendingChannel(context.Background(), subscription.WhatsApp, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Active || got.Generation != 1 || countKind(jobs, "reply") != 1 {
		t.Fatalf("non-atomic/duplicate effects: %+v jobs=%+v", got, jobs)
	}
}

func TestCommandTransactionNoDeadlock(t *testing.T) {
	svc, _, _ := testService(t)
	_, wa := identities(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := svc.ProcessCommand(ctx, notification.Command{Identity: wa, IncomingID: "bounded", Verb: "start", ReceivedAt: time.Now()})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("command callback deadlocked while the single SQLite connection was occupied")
	}
}

func TestAllowlistRemovalRequiresNewOptIn(t *testing.T) {
	svc, db, _ := testService(t)
	_, wa := identities(t)
	if err := svc.ReconcileAllowedRecipients(context.Background(), []subscription.Identity{wa}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Subscribe(context.Background(), wa); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReconcileAllowedRecipients(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	var got subscription.Subscription
	if err := db.Transaction(context.Background(), func(tx ports.Transaction) error { var e error; got, e = tx.SubscriptionByIdentity(wa); return e }); err != nil {
		t.Fatal(err)
	}
	if got.Active || got.Generation != 2 {
		t.Fatalf("removed allowlist entry must invalidate prior consent: %+v", got)
	}
	if err := svc.ReconcileAllowedRecipients(context.Background(), []subscription.Identity{wa}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Subscribe(context.Background(), wa); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(context.Background(), func(tx ports.Transaction) error { var e error; got, e = tx.SubscriptionByIdentity(wa); return e }); err != nil {
		t.Fatal(err)
	}
	if !got.Active || got.Generation != 3 {
		t.Fatalf("recipient should require a new opt-in generation: %+v", got)
	}
}

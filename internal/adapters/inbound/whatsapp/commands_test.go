package whatsapp

import (
	"context"
	"path/filepath"
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
)

var handlerElection = election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}
var handlerRecipient = "5511999999999@s.whatsapp.net"

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

type resolverFunc func(context.Context, string, string, string) (subscription.Identity, bool, error)

func (f resolverFunc) Resolve(ctx context.Context, chat, sender, alt string) (subscription.Identity, bool, error) {
	return f(ctx, chat, sender, alt)
}
func handlerFixture(t *testing.T) (*Handler, *sqlite.Store, *usecases.Service) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "handler.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id, _ := subscription.NewIdentity(handlerElection, subscription.WhatsApp, handlerRecipient)
	clock := &testClock{time.Now()}
	svc := usecases.New(db, db, simulation.New(), nil, clock, usecases.Settings{Election: handlerElection, Location: time.UTC, StaleAfter: time.Hour, DelayedAfter: time.Hour, AllowedWhatsAppRecipients: []subscription.Identity{id}})
	resolve := resolverFunc(func(_ context.Context, _, sender, _ string) (subscription.Identity, bool, error) {
		if sender != handlerRecipient {
			return subscription.Identity{}, false, nil
		}
		return id, true, nil
	})
	return NewHandler(svc, resolve, []string{handlerRecipient}, nil), db, svc
}
func commandMessage(text, id string) InboundMessage {
	return InboundMessage{MessageID: id, Text: text, ChatJID: handlerRecipient, SenderJID: handlerRecipient, ReceivedAt: time.Now()}
}
func TestPrivateAllowlistedCommands(t *testing.T) {
	h, db, _ := handlerFixture(t)
	if err := h.Handle(context.Background(), commandMessage("!start", "in-1")); err != nil {
		t.Fatal(err)
	}
	var got subscription.Subscription
	if err := db.Transaction(context.Background(), func(tx ports.Transaction) error {
		var e error
		got, e = tx.SubscriptionByIdentity(subscription.Identity{Election: handlerElection, Channel: subscription.WhatsApp, Recipient: handlerRecipient})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if !got.Active || got.Generation != 1 {
		t.Fatalf("subscription=%+v", got)
	}
	jobs, err := db.PendingChannel(context.Background(), subscription.WhatsApp, time.Now().Add(time.Minute), 20)
	if err != nil || countKind(jobs, "reply") != 1 {
		t.Fatalf("reply jobs=%+v err=%v", jobs, err)
	}
}
func TestCommandStableIDDedup(t *testing.T) {
	h, db, _ := handlerFixture(t)
	msg := commandMessage("!start", "stable-id")
	if err := h.Handle(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if err := h.Handle(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.PendingChannel(context.Background(), subscription.WhatsApp, time.Now().Add(time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	if countKind(jobs, "reply") != 1 {
		t.Fatalf("duplicate command reply: %+v", jobs)
	}
}
func TestIgnoreOwnGroupNewsletterHistoryEdit(t *testing.T) {
	h, db, _ := handlerFixture(t)
	cases := []InboundMessage{commandMessage("!start", "own"), commandMessage("!start", "group"), commandMessage("!start", "newsletter"), commandMessage("!start", "history"), commandMessage("!start", "edit")}
	cases[0].IsFromMe = true
	cases[1].IsGroup = true
	cases[2].IsNewsletter = true
	cases[3].IsHistory = true
	cases[4].IsEdit = true
	for _, m := range cases {
		if err := h.Handle(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	if jobs, err := db.PendingChannel(context.Background(), subscription.WhatsApp, time.Now().Add(time.Minute), 20); err != nil || len(jobs) != 0 {
		t.Fatalf("ignored events made deliveries: %+v %v", jobs, err)
	}
}
func TestOptOutAndAllowlistChangeDuringFlight(t *testing.T) {
	h, db, svc := handlerFixture(t)
	if err := h.Handle(context.Background(), commandMessage("!start", "start")); err != nil {
		t.Fatal(err)
	}
	if err := h.Handle(context.Background(), commandMessage("!parar", "stop")); err != nil {
		t.Fatal(err)
	}
	id, _ := subscription.NewIdentity(handlerElection, subscription.WhatsApp, handlerRecipient)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errs <- h.Handle(context.Background(), commandMessage("!start", "raced"))
	}()
	go func() { defer wg.Done(); <-start; errs <- svc.ReconcileAllowedRecipients(context.Background(), nil) }()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var sub subscription.Subscription
	if err := db.Transaction(context.Background(), func(tx ports.Transaction) error { var e error; sub, e = tx.SubscriptionByIdentity(id); return e }); err != nil {
		t.Fatal(err)
	}
	if sub.Active {
		t.Fatalf("removed allowlist recipient reactivated: %+v", sub)
	}
	jobs, err := db.PendingChannel(context.Background(), subscription.WhatsApp, time.Now().Add(time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.Kind != "reply" || job.EventKey == "command:raced" {
			t.Fatalf("allowlist removal admitted a raced command: %+v", jobs)
		}
	}
}
func TestUnrecognizedAndUnauthorizedCommandsProduceNoReplies(t *testing.T) {
	h, db, _ := handlerFixture(t)
	if err := h.Handle(context.Background(), commandMessage("hello", "text")); err != nil {
		t.Fatal(err)
	}
	unauthorized := commandMessage("!start", "unauthorized")
	unauthorized.SenderJID = "5511000000000@s.whatsapp.net"
	unauthorized.ChatJID = unauthorized.SenderJID
	if err := h.Handle(context.Background(), unauthorized); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.PendingChannel(context.Background(), subscription.WhatsApp, time.Now().Add(time.Minute), 20)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("unexpected response: %+v %v", jobs, err)
	}
}
func countKind(jobs []notification.Delivery, kind string) int {
	n := 0
	for _, j := range jobs {
		if j.Kind == kind {
			n++
		}
	}
	return n
}

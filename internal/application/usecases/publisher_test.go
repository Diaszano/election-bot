package usecases

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Diaszano/election-bot/internal/adapters/outbound/simulation"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/sqlite"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
)

type attemptStore struct {
	ports.ChannelDeliveries
	mu       sync.Mutex
	prepared int
}

func (s *attemptStore) PrepareAttempt(ctx context.Context, d notification.Delivery, o notification.Operation, id string) (notification.Attempt, error) {
	a, e := s.ChannelDeliveries.PrepareAttempt(ctx, d, o, id)
	if e == nil {
		s.mu.Lock()
		s.prepared++
		s.mu.Unlock()
	}
	return a, e
}
func (s *attemptStore) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.prepared }

type testChannelSender struct {
	mu                      sync.Mutex
	active, maxActive, sent int
	started                 chan struct{}
	block                   <-chan struct{}
	fail                    error
	store                   *attemptStore
	clock                   ports.Clock
	onNetwork               func(context.Context) error
}

func (s *testChannelSender) enter(ctx context.Context, a notification.Attempt) (notification.Acceptance, error) {
	if s.store != nil && s.store.count() == 0 {
		return notification.Acceptance{}, errors.New("network before durable attempt")
	}
	if s.onNetwork != nil {
		if err := s.onNetwork(ctx); err != nil {
			return notification.Acceptance{}, err
		}
	}
	s.mu.Lock()
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.sent++
	n := s.sent
	started := s.started
	s.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if s.block != nil {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.active--
			s.mu.Unlock()
			return notification.Acceptance{}, ctx.Err()
		case <-s.block:
		}
	}
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
	if s.fail != nil {
		return notification.Acceptance{}, s.fail
	}
	now := time.Now()
	if s.clock != nil {
		now = s.clock.Now()
	}
	return notification.Acceptance{Operation: a.Operation, AcceptedAt: now, Ref: notification.MessageRef{ID: fmt.Sprint(n), OriginalSentAt: now}}, nil
}
func (s *testChannelSender) Send(ctx context.Context, _ string, a notification.Attempt) (notification.Acceptance, error) {
	return s.enter(ctx, a)
}
func (s *testChannelSender) Edit(ctx context.Context, _ string, _ notification.MessageRef, a notification.Attempt) (notification.Acceptance, error) {
	return s.enter(ctx, a)
}

func publisherFixture(t *testing.T, c *clock, channel subscription.Channel, fake *testChannelSender, opts ...PublisherOption) (*Publisher, *sqlite.Store, subscription.Identity) {
	t.Helper()
	db, e := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "publisher.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	identity := subscription.Identity{Election: testElection, Channel: channel, Recipient: "123"}
	if channel == subscription.WhatsApp {
		identity.Recipient = "5511999999999@s.whatsapp.net"
	}
	if e = New(db, db, simulation.New(), &sender{}, c, Settings{Election: testElection, Location: time.UTC, StaleAfter: time.Hour, DelayedAfter: time.Hour}).Subscribe(context.Background(), identity); e != nil {
		t.Fatal(e)
	}
	snap := simulation.Snapshot(25)
	if e = New(db, db, simulation.New(), &sender{}, c, Settings{Election: testElection, Location: time.UTC, StaleAfter: time.Hour, DelayedAfter: time.Hour}).ProcessElectionSnapshot(context.Background(), ports.FetchResult{Snapshot: &snap}); e != nil {
		t.Fatal(e)
	}
	store := &attemptStore{ChannelDeliveries: db}
	fake.store = store
	fake.clock = c
	p := NewPublisher(channel, store, fake, c, notification.Policy{}, opts...)
	return p, db, identity
}

func TestSendRequiresCommittedAttempt(t *testing.T) {
	c := &clock{time.Now()}
	sender := &testChannelSender{}
	p, _, _ := publisherFixture(t, c, subscription.WhatsApp, sender)
	if err := p.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sender.sent == 0 {
		t.Fatal("no send")
	}
}
func TestNoTransactionDuringNetwork(t *testing.T) {
	c := &clock{time.Now()}
	sender := &testChannelSender{}
	p, db, _ := publisherFixture(t, c, subscription.WhatsApp, sender)
	sender.onNetwork = func(ctx context.Context) error {
		done := make(chan error, 1)
		go func() { _, e := db.Latest(ctx); done <- e }()
		select {
		case e := <-done:
			return e
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Publish(ctx); err != nil {
		t.Fatal(err)
	}
}

type fixedProvider struct{ result ports.FetchResult }

func (f fixedProvider) Fetch(context.Context, ports.Validators) (ports.FetchResult, error) {
	return f.result, nil
}

func TestBlockedWhatsAppDoesNotBlockTelegramOrPoll(t *testing.T) {
	ctx := context.Background()
	c := &clock{time.Now()}
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "shared.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tgID, waID := identities(t)
	waID.Election = testElection
	svc := New(db, db, fixedProvider{}, &sender{}, c, Settings{Election: testElection, Location: time.UTC, StaleAfter: time.Hour, DelayedAfter: time.Hour})
	if err = svc.Subscribe(ctx, tgID); err != nil {
		t.Fatal(err)
	}
	if err = svc.Subscribe(ctx, waID); err != nil {
		t.Fatal(err)
	}
	initial := simulation.Snapshot(25)
	if err = svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &initial}); err != nil {
		t.Fatal(err)
	}
	updated := simulation.Snapshot(50)
	updated.ID = "poll-generation"
	updated.Published = initial.Published.Add(time.Second)
	updated.Generated = initial.Generated.Add(time.Second)
	svc.provider = fixedProvider{ports.FetchResult{Snapshot: &updated}}
	release := make(chan struct{})
	wa := &testChannelSender{block: release, started: make(chan struct{}, 1)}
	tg := &testChannelSender{}
	waStore, tgStore := &attemptStore{ChannelDeliveries: db}, &attemptStore{ChannelDeliveries: db}
	wa.store, tg.store = waStore, tgStore
	wa.clock, tg.clock = c, c
	wp := NewPublisher(subscription.WhatsApp, waStore, wa, c, notification.Policy{})
	tp := NewPublisher(subscription.Telegram, tgStore, tg, c, notification.Policy{})
	blocked := make(chan error, 1)
	go func() { blocked <- wp.Publish(ctx) }()
	select {
	case <-wa.started:
	case <-time.After(time.Second):
		t.Fatal("WhatsApp sender did not block")
	}
	if err = tp.Publish(ctx); err != nil {
		close(release)
		t.Fatal(err)
	}
	if err = svc.PollElectionResults(ctx); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err = <-blocked; err != nil {
		t.Fatal(err)
	}
}
func TestPerRecipientSerialization(t *testing.T) {
	c := &clock{time.Now()}
	release := make(chan struct{})
	sender := &testChannelSender{block: release, started: make(chan struct{}, 2)}
	p, _, _ := publisherFixture(t, c, subscription.WhatsApp, sender)
	done := make(chan error, 2)
	go func() { done <- p.Publish(context.Background()) }()
	select {
	case <-sender.started:
	case <-time.After(time.Second):
		t.Fatal("first publish did not start")
	}
	go func() { done <- p.Publish(context.Background()) }()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	sender.mu.Lock()
	maxActive := sender.maxActive
	sender.mu.Unlock()
	if maxActive != 1 {
		t.Fatalf("concurrent sends=%d", maxActive)
	}
}
func TestRetryExhaustionRetainsWork(t *testing.T) {
	c := &clock{time.Now()}
	sender := &testChannelSender{fail: &ports.ExternalError{Kind: "transient"}}
	p, db, _ := publisherFixture(t, c, subscription.WhatsApp, sender, WithMaxAttempts(2))
	for i := 0; i < 3; i++ {
		c.t = c.t.Add(10 * time.Minute)
		if err := p.Publish(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RequeueFailed(context.Background(), subscription.WhatsApp); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.PendingChannel(context.Background(), subscription.WhatsApp, c.Now().Add(time.Hour), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) == 0 {
		t.Fatal("exhausted work was deleted")
	}
}
func TestNewRevisionAfterAmbiguousAttempt(t *testing.T) {
	c := &clock{time.Now()}
	fake := &testChannelSender{fail: &ports.ExternalError{Kind: "transient"}}
	p, db, identity := publisherFixture(t, c, subscription.WhatsApp, fake, WithMaxAttempts(3))
	if err := p.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap := simulation.Snapshot(50)
	if err := New(db, db, simulation.New(), &sender{}, c, Settings{Election: testElection, Location: time.UTC, StaleAfter: time.Hour, DelayedAfter: time.Hour}).ProcessElectionSnapshot(context.Background(), ports.FetchResult{Snapshot: &snap}); err != nil {
		t.Fatal(err)
	}
	if err := db.RequeueFailed(context.Background(), identity.Channel); err != nil {
		t.Fatal(err)
	}
	fake.fail = nil
	c.t = c.t.Add(10 * time.Minute)
	if err := p.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.store.count() < 2 {
		t.Fatal("new revision did not prepare durable attempts")
	}
}

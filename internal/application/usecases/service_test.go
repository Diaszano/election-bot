package usecases

import (
	"context"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/simulation"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/sqlite"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
	"path/filepath"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

type sender struct {
	sends, edits int
	fail         bool
}

func (s *sender) Send(ctx context.Context, chat int64, text string) (int64, error) {
	if s.fail {
		return 0, &ports.ExternalError{Kind: "transient"}
	}
	s.sends++
	return int64(s.sends), nil
}
func (s *sender) Edit(ctx context.Context, chat, id int64, text string) error {
	if s.fail {
		return &ports.ExternalError{Kind: "transient"}
	}
	s.edits++
	return nil
}
func TestSnapshotValidatorsMilestonesAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db, e := sqlite.Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	cfg := Settings{Location: time.UTC, PollInterval: 10 * time.Second, BeforeInterval: 5 * time.Minute, FinalInterval: time.Hour, StaleAfter: 2 * time.Minute, DelayedAfter: 5 * time.Minute}
	c := &clock{time.Now()}
	send := &sender{}
	svc := New(db, db, simulation.New(), send, c, cfg)
	if e = svc.SubscribeUser(ctx, 1); e != nil {
		t.Fatal(e)
	}
	s := simulation.Snapshot(25)
	if e = svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &s, Validators: ports.Validators{ETag: "one"}}); e != nil {
		t.Fatal(e)
	}
	if e = svc.PublishResultUpdates(ctx); e != nil {
		t.Fatal(e)
	}
	if send.sends != 2 {
		t.Fatalf("sends=%d", send.sends)
	}
	if e = svc.SubscribeUser(ctx, 1); e != nil {
		t.Fatal(e)
	}
	if e = svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &s, Validators: ports.Validators{ETag: "two"}}); e != nil {
		t.Fatal(e)
	}
	svc.PublishResultUpdates(ctx)
	if send.sends != 2 || send.edits != 0 {
		t.Fatal("unchanged duplicate")
	}
	db.Close()
	db, e = sqlite.Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc = New(db, db, simulation.New(), send, c, cfg)
	saved, e := db.Latest(ctx)
	if e != nil || saved.Validators.ETag != "two" {
		t.Fatal(saved, e)
	}
	if e = svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Unchanged: true, Validators: saved.Validators}); e != nil {
		t.Fatal(e)
	}
	s = simulation.Snapshot(76)
	if e = svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &s}); e != nil {
		t.Fatal(e)
	}
	svc.PublishResultUpdates(ctx)
	if send.sends != 4 || send.edits != 1 {
		t.Fatal(send)
	}
	svc.PublishResultUpdates(ctx)
	if send.sends != 4 {
		t.Fatal("duplicate milestone")
	}
}
func TestPrivateSubscriptionAndDurableFailure(t *testing.T) {
	ctx := context.Background()
	db, e := sqlite.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	cfg := Settings{Location: time.UTC, PollInterval: 10 * time.Second, BeforeInterval: 5 * time.Minute, FinalInterval: time.Hour, StaleAfter: 2 * time.Minute, DelayedAfter: 5 * time.Minute}
	c := &clock{time.Now()}
	send := &sender{fail: true}
	svc := New(db, db, simulation.New(), send, c, cfg)
	if svc.SubscribeUser(ctx, -1) == nil {
		t.Fatal("negative group accepted")
	}
	svc.SubscribeUser(ctx, 1)
	s := simulation.Snapshot(50)
	svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &s})
	svc.PublishResultUpdates(ctx)
	c.t = c.t.Add(10 * time.Minute)
	jobs, e := db.Pending(ctx, c.t, 10)
	if e != nil || len(jobs) != 3 {
		t.Fatal(jobs, e)
	}
	send.fail = false
	svc.PublishResultUpdates(ctx)
	if send.sends != 3 {
		t.Fatal(send)
	}
}

func TestRepeatedStartPreservesBaselineAndGeneration(t *testing.T) {
	ctx := context.Background()
	db, e := sqlite.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := simulation.Snapshot(50)
	c := &clock{s.Published}
	svc := New(db, db, simulation.New(), &sender{}, c, Settings{Location: time.UTC, StaleAfter: time.Hour, DelayedAfter: time.Hour})
	if e = svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &s}); e != nil {
		t.Fatal(e)
	}
	if e = svc.SubscribeUser(ctx, 1); e != nil {
		t.Fatal(e)
	}
	var before subscription.Subscription
	db.Transaction(ctx, func(tx ports.Transaction) error { before, e = tx.Subscription(1); return e })
	if before.Baseline != 50 || before.Generation != 1 {
		t.Fatal(before)
	}
	s = simulation.Snapshot(75)
	svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &s})
	svc.SubscribeUser(ctx, 1)
	db.Transaction(ctx, func(tx ports.Transaction) error {
		after, e := tx.Subscription(1)
		if e != nil || after != before {
			t.Fatal(before, after, e)
		}
		return e
	})
}

type stoppingSender struct {
	svc  *Service
	sent int
}

func (s *stoppingSender) Send(ctx context.Context, chat int64, text string) (int64, error) {
	s.sent++
	if e := s.svc.UnsubscribeUser(ctx, chat); e != nil {
		return 0, e
	}
	return 1, nil
}
func (s *stoppingSender) Edit(context.Context, int64, int64, string) error { return nil }
func TestUnsubscribeStopsRemainingBatch(t *testing.T) {
	ctx := context.Background()
	db, e := sqlite.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	c := &clock{simulation.Snapshot(50).Published}
	send := &stoppingSender{}
	svc := New(db, db, simulation.New(), send, c, Settings{Location: time.UTC, StaleAfter: time.Hour, DelayedAfter: time.Hour})
	send.svc = svc
	svc.SubscribeUser(ctx, 1)
	snapshot := simulation.Snapshot(50)
	svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &snapshot})
	if e = svc.PublishResultUpdates(ctx); e != nil {
		t.Fatal(e)
	}
	if send.sent != 1 {
		t.Fatalf("sent %d after unsubscribe", send.sent)
	}
}
func TestOldEditCompletionKeepsNewRevision(t *testing.T) {
	ctx := context.Background()
	db, e := sqlite.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	c := &clock{simulation.Snapshot(25).Published}
	svc := New(db, db, simulation.New(), &sender{}, c, Settings{Location: time.UTC, StaleAfter: time.Hour, DelayedAfter: time.Hour})
	svc.SubscribeUser(ctx, 1)
	snapshot := simulation.Snapshot(25)
	svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &snapshot})
	jobs, e := db.Pending(ctx, c.Now(), 10)
	if e != nil {
		t.Fatal(e)
	}
	old := jobs[0]
	snapshot = simulation.Snapshot(50)
	svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &snapshot})
	if e = db.Complete(ctx, old, 42, c.Now()); e != nil {
		t.Fatal(e)
	}
	jobs, e = db.Pending(ctx, c.Now(), 10)
	if e != nil {
		t.Fatal(e)
	}
	if len(jobs) != 3 || jobs[0].Revision == old.Revision || jobs[0].MessageID != 42 {
		t.Fatal(jobs)
	}
}

type failingProvider struct{}

func (failingProvider) Fetch(context.Context, ports.Validators) (ports.FetchResult, error) {
	return ports.FetchResult{}, &ports.ExternalError{Kind: "not_published", RetryAfter: 15 * time.Minute}
}
func TestUpstreamFailureRetainsSnapshotAndCooldown(t *testing.T) {
	ctx := context.Background()
	db, e := sqlite.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	c := &clock{simulation.Snapshot(25).Published}
	svc := New(db, db, failingProvider{}, &sender{}, c, Settings{Location: time.UTC, PollInterval: 10 * time.Second, BeforeInterval: 5 * time.Minute, FinalInterval: time.Hour, StaleAfter: 2 * time.Minute, DelayedAfter: 5 * time.Minute})
	snapshot := simulation.Snapshot(25)
	svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &snapshot, Validators: ports.Validators{ETag: "good"}})
	if svc.PollElectionResults(ctx) == nil {
		t.Fatal("upstream error hidden")
	}
	saved, e := db.Latest(ctx)
	if e != nil || saved.Validators.ETag != "good" {
		t.Fatal(saved, e)
	}
	status, e := db.Status(ctx)
	if e != nil || status.Failures != 1 || status.NextPoll.Sub(c.Now()) < 15*time.Minute {
		t.Fatal(status, e)
	}
	if e = svc.PollElectionResults(ctx); e != nil {
		t.Fatal("cooldown fetched again")
	}
}

func TestUnchanged200ClearsStaleMessage(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := &clock{time.Now()}
	send := &sender{}
	svc := New(db, db, simulation.New(), send, c, Settings{Location: time.UTC, StaleAfter: time.Minute, DelayedAfter: time.Hour})
	s := simulation.Snapshot(0)
	s.Published = c.Now()
	s.Generated = c.Now()
	if err = svc.SubscribeUser(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err = svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &s}); err != nil {
		t.Fatal(err)
	}
	if err = svc.PublishResultUpdates(ctx); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(2 * time.Minute)
	if err = svc.PublishResultUpdates(ctx); err != nil {
		t.Fatal(err)
	}
	if send.edits != 1 {
		t.Fatalf("stale edits=%d", send.edits)
	}
	if err = svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &s, Validators: ports.Validators{ETag: "renewed"}}); err != nil {
		t.Fatal(err)
	}
	if err = svc.PublishResultUpdates(ctx); err != nil {
		t.Fatal(err)
	}
	if send.edits != 2 {
		t.Fatalf("recovery edits=%d, want 2", send.edits)
	}
}

type recoveringProvider struct {
	calls    int
	failures int
	kind     string
	snapshot election.Snapshot
}

func (p *recoveringProvider) Fetch(context.Context, ports.Validators) (ports.FetchResult, error) {
	p.calls++
	if p.calls <= p.failures {
		return ports.FetchResult{}, &ports.ExternalError{Kind: p.kind}
	}
	return ports.FetchResult{Snapshot: &p.snapshot}, nil
}
func TestPollingRetriesAndAcceptedStatusInterval(t *testing.T) {
	for _, tc := range []struct {
		name, kind          string
		failures, wantCalls int
		final               bool
		wantInterval        time.Duration
	}{
		{"transient recovery", "transient", 2, 3, false, 10 * time.Second},
		{"bounded failures", "transient", 10, 3, false, 10 * time.Second},
		{"missing endpoint", "not_published", 10, 1, false, 5 * time.Minute},
		{"new finalization", "transient", 0, 1, true, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			c := &clock{time.Now()}
			s := simulation.Snapshot(100)
			if tc.final {
				s.Status = election.Finalized
				s.Source = "official" // Synthetic domain state, never an external status mapping.
			}
			p := &recoveringProvider{failures: tc.failures, kind: tc.kind, snapshot: s}
			svc := New(db, db, p, &sender{}, c, Settings{Location: time.UTC, PollInterval: 10 * time.Second, BeforeInterval: 5 * time.Minute, FinalInterval: time.Hour, CountingStart: c.Now().Add(time.Hour)})
			err = svc.PollElectionResults(ctx)
			if tc.failures < 3 && err != nil {
				t.Fatal(err)
			}
			if p.calls != tc.wantCalls {
				t.Fatalf("calls=%d want %d", p.calls, tc.wantCalls)
			}
			st, e := db.Status(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if tc.failures < 3 && st.NextPoll.Sub(c.Now()) != tc.wantInterval {
				t.Fatalf("interval=%s want %s", st.NextPoll.Sub(c.Now()), tc.wantInterval)
			}
		})
	}
}

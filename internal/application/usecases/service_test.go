package usecases

import (
	"context"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/simulation"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/sqlite"
	"github.com/Diaszano/election-bot/internal/application/ports"
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

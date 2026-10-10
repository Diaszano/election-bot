package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/simulation"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/sqlite"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/application/usecases"
	"sync"
	"time"
)

// RecordingSender is the explicit offline transport; it performs no remote delivery.
type RecordingSender struct {
	mu           sync.Mutex
	Sent, Edited int
	Fail         bool
}

func (s *RecordingSender) Send(ctx context.Context, chat int64, text string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail {
		return 0, &ports.ExternalError{Kind: "transient"}
	}
	s.Sent++
	return int64(s.Sent), ctx.Err()
}
func (s *RecordingSender) Edit(ctx context.Context, chat, id int64, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail {
		return &ports.ExternalError{Kind: "transient"}
	}
	s.Edited++
	return ctx.Err()
}

type simulationClock struct{ now time.Time }

func (c *simulationClock) Now() time.Time { return c.now }

// Simulate executes a finite durable recovery self-check against a fresh database.
func Simulate(ctx context.Context, path string) error {
	db, e := sqlite.Open(ctx, path)
	if e != nil {
		return e
	}
	defer func() { db.Close() }()
	clock := &simulationClock{simulation.Snapshot(0).Published}
	sender := &RecordingSender{}
	settings := usecases.Settings{Location: time.UTC, PollInterval: 10 * time.Second, BeforeInterval: 5 * time.Minute, FinalInterval: time.Hour, StaleAfter: 2 * time.Minute, DelayedAfter: 5 * time.Minute}
	makeService := func() *usecases.Service { return usecases.New(db, db, simulation.New(), sender, clock, settings) }
	svc := makeService()
	if e = svc.SubscribeUser(ctx, 1); e != nil {
		return e
	}
	for _, progress := range []int{0, 25, 75, 100} {
		snapshot := simulation.Snapshot(progress)
		clock.now = snapshot.Published
		if e = svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &snapshot}); e != nil {
			return e
		}
		if e = svc.PublishResultUpdates(ctx); e != nil {
			return e
		}
	}
	if sender.Sent != 7 || sender.Edited != 3 {
		return fmt.Errorf("unexpected simulation deliveries: sent=%d edited=%d", sender.Sent, sender.Edited)
	}
	snapshot := simulation.Snapshot(100)
	if e = svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Snapshot: &snapshot, Validators: ports.Validators{ETag: "changed-validator"}}); e != nil {
		return e
	}
	if e = svc.PublishResultUpdates(ctx); e != nil {
		return e
	}
	if sender.Sent != 7 || sender.Edited != 3 {
		return errors.New("unchanged snapshot duplicated delivery")
	}
	if e = db.Close(); e != nil {
		return e
	}
	db, e = sqlite.Open(ctx, path)
	if e != nil {
		return e
	}
	svc = makeService()
	saved, e := db.Latest(ctx)
	if e != nil || saved.Validators.ETag != "changed-validator" {
		return errors.New("restart lost validators")
	}
	if e = svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Unchanged: true, Validators: saved.Validators}); e != nil {
		return e
	}
	sender.Fail = true
	clock.now = clock.now.Add(3 * time.Minute)
	if e = svc.PublishResultUpdates(ctx); e != nil {
		return e
	}
	clock.now = clock.now.Add(10 * time.Minute)
	pending, e := db.Pending(ctx, clock.now, 100)
	if e != nil || len(pending) != 1 {
		return errors.New("failed update was not durable")
	}
	sender.Fail = false
	if e = svc.ProcessElectionSnapshot(ctx, ports.FetchResult{Unchanged: true, Validators: saved.Validators}); e != nil {
		return e
	}
	if e = svc.PublishResultUpdates(ctx); e != nil {
		return e
	}
	pending, e = db.Pending(ctx, clock.now.Add(time.Hour), 100)
	if e != nil || len(pending) != 0 {
		return errors.New("pending delivery recovery failed")
	}
	return nil
}

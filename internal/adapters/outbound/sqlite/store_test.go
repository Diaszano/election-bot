package sqlite

import (
	"context"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
	"path/filepath"
	"testing"
	"time"
)

func TestTransactionRollbackAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	s, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	e = s.Transaction(ctx, func(tx ports.Transaction) error {
		return tx.PutSubscription(subscription.Subscription{ChatID: 1, Active: true, Generation: 1, ResultsEnabled: true, MilestonesEnabled: true})
	})
	if e != nil {
		t.Fatal(e)
	}
	e = s.Transaction(ctx, func(tx ports.Transaction) error {
		if e := tx.Enqueue(notification.Delivery{ChatID: 1, Generation: 1, Kind: "milestone", Milestone: 25, Revision: "a", Text: "x", Due: time.Now()}); e != nil {
			return e
		}
		return ports.ErrNoSnapshot
	})
	if e == nil {
		t.Fatal("missing rollback")
	}
	jobs, e := s.Pending(ctx, time.Now(), 10)
	if e != nil || len(jobs) != 0 {
		t.Fatal(jobs, e)
	}
	s.Close()
	s, e = Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	e = s.Transaction(ctx, func(tx ports.Transaction) error {
		sub, e := tx.Subscription(1)
		if e != nil || !sub.Active {
			t.Fatal(sub, e)
		}
		for range 2 {
			if e := tx.Enqueue(notification.Delivery{ChatID: 1, Generation: 1, Kind: "milestone", Milestone: 25, Revision: "a", Text: "x", Due: time.Now()}); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	jobs, e = s.Pending(ctx, time.Now(), 10)
	if e != nil || len(jobs) != 1 {
		t.Fatal(jobs, e)
	}
	if e = s.Complete(ctx, jobs[0], 10, time.Now()); e != nil {
		t.Fatal(e)
	}
	jobs, e = s.Pending(ctx, time.Now(), 10)
	if e != nil || len(jobs) != 0 {
		t.Fatal(jobs, e)
	}
}

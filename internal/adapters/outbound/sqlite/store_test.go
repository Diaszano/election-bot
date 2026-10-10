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

func TestOldFailureCannotRemoveNewGeneration(t *testing.T) {
	for _, kind := range []string{"payload", "blocked", "invalid_chat"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			save := func(g int64) error {
				return db.Transaction(ctx, func(tx ports.Transaction) error {
					if err := tx.PutSubscription(subscription.Subscription{ChatID: 1, Active: true, Generation: g}); err != nil {
						return err
					}
					return tx.Enqueue(notification.Delivery{ChatID: 1, Generation: g, Kind: "main", Revision: "same", Text: "text", Due: time.Now()})
				})
			}
			if err = save(1); err != nil {
				t.Fatal(err)
			}
			jobs, err := db.Pending(ctx, time.Now(), 10)
			if err != nil || len(jobs) != 1 {
				t.Fatal(jobs, err)
			}
			old := jobs[0]
			if err = save(2); err != nil {
				t.Fatal(err)
			}
			if err = db.MarkUndeliverable(ctx, old, kind); err != nil {
				t.Fatal(err)
			}
			jobs, err = db.Pending(ctx, time.Now(), 10)
			if err != nil || len(jobs) != 1 || jobs[0].Generation != 2 {
				t.Fatal("new generation lost", jobs, err)
			}
		})
	}
}

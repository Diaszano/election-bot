package lifecycle

import (
	"context"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/sqlite"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
	"github.com/Diaszano/election-bot/internal/platform/config"
	"path/filepath"
	"testing"
	"time"
)

func TestDisabledTelegramRetainsPendingAlerts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Transaction(ctx, func(tx ports.Transaction) error {
		if err := tx.PutSubscription(subscription.Subscription{ChatID: 1, Active: true, Generation: 1}); err != nil {
			return err
		}
		return tx.Enqueue(notification.Delivery{ChatID: 1, Generation: 1, Kind: "milestone", Milestone: 25, Revision: "m25", Text: "text", Due: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	t.Setenv("SQLITE_PATH", path)
	t.Setenv("HTTP_LISTEN_ADDRESS", "127.0.0.1:0")
	t.Setenv("TELEGRAM_ENABLED", "false")
	t.Setenv("OTEL_ENABLED", "false")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	if err = Run(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	jobs, err := db.Pending(context.Background(), time.Now(), 10)
	if err != nil || len(jobs) != 1 {
		t.Fatal("disabled delivery consumed pending milestone", jobs, err)
	}
}

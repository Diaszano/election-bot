package lifecycle

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Diaszano/election-bot/internal/adapters/outbound/sqlite"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
	"github.com/Diaszano/election-bot/internal/platform/config"
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

func TestWhatsAppUnavailablePreservesRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	t.Setenv("SQLITE_PATH", path)
	t.Setenv("HTTP_LISTEN_ADDRESS", "127.0.0.1:0")
	t.Setenv("TELEGRAM_ENABLED", "false")
	t.Setenv("WHATSAPP_ENABLED", "true")
	t.Setenv("WHATSAPP_ALLOWED_RECIPIENTS", "5511999999999@s.whatsapp.net")
	t.Setenv("WHATSAPP_SESSION_DB_PATH", filepath.Join(t.TempDir(), "whatsapp.db"))
	t.Setenv("OTEL_ENABLED", "false")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err = Run(ctx, cfg); err != nil {
		t.Fatalf("WhatsApp unavailable stopped the application: %v", err)
	}
	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	state, err := db.ReadChannelState(context.Background(), subscription.WhatsApp)
	if err != nil {
		t.Fatal(err)
	}
	if !state.AuthenticationRequired || state.PauseReason != "not_paired" {
		t.Fatalf("unpaired session was not durably suspended: %+v", state)
	}
	if _, err = db.Latest(context.Background()); err != nil {
		t.Fatalf("election polling stopped with WhatsApp unavailable: %v", err)
	}
}

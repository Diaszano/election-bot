package lifecycle

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Diaszano/election-bot/internal/adapters/outbound/sqlite"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
	"github.com/Diaszano/election-bot/internal/platform/config"
)

func TestChangedWhatsAppAccountSuspendsConsent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	cfg := config.Config{Database: path}
	if err := bindWhatsAppAccount(ctx, cfg, "5511999999999@s.whatsapp.net"); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := subscription.NewIdentity(election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}, subscription.WhatsApp, "5511888888888@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Transaction(ctx, func(tx ports.Transaction) error {
		return tx.PutSubscription(subscription.Subscription{Identity: identity, Active: true, Generation: 1, ResultsEnabled: true, MilestonesEnabled: true})
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if err = bindWhatsAppAccount(ctx, cfg, "5511777777777@s.whatsapp.net"); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state, err := store.ReadChannelState(ctx, subscription.WhatsApp)
	if err != nil {
		t.Fatal(err)
	}
	if !state.AuthenticationRequired || state.AccountBinding == "5511777777777@s.whatsapp.net" {
		t.Fatalf("account change was not suspended/private: %+v", state)
	}
	var sub subscription.Subscription
	if err = store.Transaction(ctx, func(tx ports.Transaction) error { var e error; sub, e = tx.SubscriptionByIdentity(identity); return e }); err != nil {
		t.Fatal(err)
	}
	if sub.Active || sub.Generation != 2 {
		t.Fatalf("old consent remained active: %+v", sub)
	}
	if err = bindWhatsAppAccount(ctx, cfg, "5511777777777@s.whatsapp.net"); err != nil {
		t.Fatal(err)
	}
	state, err = store.ReadChannelState(ctx, subscription.WhatsApp)
	if err != nil || state.AuthenticationRequired {
		t.Fatalf("same-account pairing did not resume: %+v %v", state, err)
	}
}

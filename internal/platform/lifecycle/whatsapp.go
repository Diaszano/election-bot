package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Diaszano/election-bot/internal/adapters/outbound/sqlite"
	waadapter "github.com/Diaszano/election-bot/internal/adapters/outbound/whatsapp"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/application/services"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
	"github.com/Diaszano/election-bot/internal/platform/config"
)

func PairWhatsApp(ctx context.Context, c config.Config, out io.Writer, terminal bool, render waadapter.QRRenderer) (err error) {
	finish := func(error) {}
	if c.WhatsAppTelemetryEnabled {
		ctx, finish = services.Operation(ctx, "whatsapp.pair")
	}
	defer func() {
		if err != nil {
			finish(errors.New("WhatsApp pairing failed"))
		} else {
			finish(nil)
		}
	}()
	session, err := waadapter.OpenSession(ctx, c.WhatsAppSessionDB)
	if err != nil {
		return err
	}
	client := waadapter.NewClient(session, c.WhatsAppConnectTimeout)
	defer func() {
		if closeErr := client.Close(); err == nil {
			err = closeErr
		}
	}()
	if err = waadapter.Pair(ctx, client, session, out, terminal, render); err != nil {
		return err
	}
	return bindWhatsAppAccount(ctx, c, session.Device().GetJID().String())
}

func LogoutWhatsApp(ctx context.Context, c config.Config) (err error) {
	finish := func(error) {}
	if c.WhatsAppTelemetryEnabled {
		ctx, finish = services.Operation(ctx, "whatsapp.disconnect")
	}
	defer func() {
		if err != nil {
			finish(errors.New("WhatsApp logout failed"))
		} else {
			finish(nil)
		}
	}()
	session, err := waadapter.OpenSession(ctx, c.WhatsAppSessionDB)
	if err != nil {
		return err
	}
	client := waadapter.NewClient(session, c.WhatsAppConnectTimeout)
	defer func() {
		if closeErr := client.Close(); err == nil {
			err = closeErr
		}
	}()
	if !client.Paired() {
		return errors.New("WhatsApp session is not paired")
	}
	connectCtx, cancel := context.WithTimeout(ctx, c.WhatsAppConnectTimeout)
	err = client.Connect(connectCtx)
	cancel()
	if err != nil {
		return errors.New("could not connect WhatsApp for logout")
	}
	if err = waitForConnected(ctx, client.Events()); err != nil {
		return err
	}
	if err = client.Logout(ctx); err != nil {
		return err
	}
	store, err := sqlite.Open(ctx, c.Database)
	if err != nil {
		return err
	}
	defer store.Close()
	state, err := store.ReadChannelState(ctx, subscription.WhatsApp)
	if err != nil {
		return err
	}
	state.AuthenticationRequired = true
	state.PauseReason = "operator_logout"
	return store.SaveChannelState(ctx, subscription.WhatsApp, state)
}

func waitForConnected(ctx context.Context, events <-chan waadapter.ConnectionEvent) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return errors.New("WhatsApp connection closed")
			}
			switch event.Kind {
			case waadapter.ConnectionConnected:
				return nil
			case waadapter.ConnectionAuthenticationRequired:
				return errors.New("WhatsApp authentication required")
			case waadapter.ConnectionRestricted, waadapter.ConnectionOutdated:
				return errors.New("WhatsApp connection unavailable")
			}
		}
	}
}

func bindWhatsAppAccount(ctx context.Context, c config.Config, jid string) error {
	if jid == "" {
		return errors.New("paired WhatsApp account has no identity")
	}
	digest := sha256.Sum256([]byte(jid))
	binding := hex.EncodeToString(digest[:])
	store, err := sqlite.Open(ctx, c.Database)
	if err != nil {
		return err
	}
	defer store.Close()
	state, err := store.ReadChannelState(ctx, subscription.WhatsApp)
	if err != nil {
		return err
	}
	if state.AccountBinding != "" && state.AccountBinding != binding {
		identity := election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}
		if err = store.Transaction(ctx, func(tx ports.Transaction) error {
			subs, e := tx.SubscriptionsFor(identity, subscription.WhatsApp)
			if e != nil {
				return e
			}
			for _, sub := range subs {
				sub.Active = false
				sub.Generation++
				if e = tx.PutSubscription(sub); e != nil {
					return e
				}
				if e = tx.CancelIdentity(sub.Identity); e != nil {
					return e
				}
			}
			return nil
		}); err != nil {
			return err
		}
		state.AuthenticationRequired = true
		state.PauseReason = "account_changed"
	} else {
		state.AuthenticationRequired = false
		state.PausedUntil = time.Time{}
		state.PauseReason = "connected"
	}
	state.AccountBinding = binding
	if err = store.SaveChannelState(ctx, subscription.WhatsApp, state); err != nil {
		return fmt.Errorf("save WhatsApp account state: %w", err)
	}
	return nil
}

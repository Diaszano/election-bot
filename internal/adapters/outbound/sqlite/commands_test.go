package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
)

func TestCommandTransactionDedup(t *testing.T) {
	ctx := context.Background()
	store := newWAStore(t, "commands.db")
	identity := newWAIdentity()
	command := notification.Command{Identity: identity, IncomingID: "stable-message", Verb: "start", ReceivedAt: time.Now()}
	apply := func(tx ports.Transaction) error {
		sub, err := tx.SubscriptionByIdentity(identity)
		if err != nil {
			return err
		}
		sub.Active = true
		sub.Generation++
		sub.ResultsEnabled = true
		sub.MilestonesEnabled = true
		if err = tx.PutSubscription(sub); err != nil {
			return err
		}
		sub, err = tx.SubscriptionByIdentity(identity)
		if err != nil {
			return err
		}
		if err = tx.AuditSubscription(sub, "opt_in", command.IncomingID, command.ReceivedAt); err != nil {
			return err
		}
		return tx.Enqueue(notification.Delivery{Identity: identity, Generation: sub.Generation, Kind: "reply", EventKey: "command:" + command.IncomingID, Revision: "reply-1", Text: "Inscrito", Due: command.ReceivedAt})
	}
	processed, err := store.ProcessCommand(ctx, command, apply)
	if err != nil || !processed {
		t.Fatalf("first command processed=%t err=%v", processed, err)
	}
	processed, err = store.ProcessCommand(ctx, command, apply)
	if err != nil || processed {
		t.Fatalf("duplicate command processed=%t err=%v", processed, err)
	}
	var active, generation, audits, replies int
	if err = store.db.QueryRowContext(ctx, `SELECT active,generation FROM subscriptions WHERE channel=?`, subscription.WhatsApp).Scan(&active, &generation); err != nil {
		t.Fatal(err)
	}
	if err = store.db.QueryRowContext(ctx, `SELECT count(*) FROM subscription_audit`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err = store.db.QueryRowContext(ctx, `SELECT count(*) FROM outbox WHERE kind='reply'`).Scan(&replies); err != nil {
		t.Fatal(err)
	}
	if active != 1 || generation != 1 || audits != 1 || replies != 1 {
		t.Fatalf("command effects duplicated: active=%d generation=%d audits=%d replies=%d", active, generation, audits, replies)
	}
}

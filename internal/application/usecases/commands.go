package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/application/services"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
)

// ProcessCommand records WhatsApp consent and its response atomically. Its callback
// uses only the supplied transaction; network I/O remains in the channel dispatcher.
func (s *Service) ProcessCommand(ctx context.Context, command notification.Command) (bool, error) {
	if command.Identity.Channel != subscription.WhatsApp || command.Identity.Election != s.settings.Election {
		return false, errors.New("WhatsApp command identity is invalid")
	}
	if _, err := subscription.NewIdentity(command.Identity.Election, command.Identity.Channel, command.Identity.Recipient); err != nil {
		return false, err
	}
	store, ok := s.deliveries.(ports.CommandStore)
	if !ok {
		return false, errors.New("command transaction store is unavailable")
	}
	return store.ProcessCommand(ctx, command, func(tx ports.Transaction) error {
		sub, err := tx.SubscriptionByIdentity(command.Identity)
		if err != nil {
			return err
		}
		var reply string
		switch command.Verb {
		case "start":
			if !sub.Active {
				if err = s.subscribeTx(tx, command.Identity, command.IncomingID, command.ReceivedAt); err != nil {
					return err
				}
				sub, err = tx.SubscriptionByIdentity(command.Identity)
				if err != nil {
					return err
				}
			}
			reply = "Inscrição ativada. Você receberá atualizações da apuração."
		case "parar":
			if sub.Active {
				if err = s.unsubscribeTx(tx, command.Identity, command.IncomingID, command.ReceivedAt); err != nil {
					return err
				}
			}
			reply = "Inscrição cancelada. Você não receberá novas atualizações."
		case "agora":
			latest, e := tx.Latest()
			if errors.Is(e, ports.ErrNoSnapshot) {
				reply = "Ainda não há resultados disponíveis."
			} else if e != nil {
				return e
			} else {
				status, e := tx.Status()
				if e != nil {
					return e
				}
				reply = services.FormatWhatsAppResults(latest.Snapshot, s.settings.Location, s.freshness(latest.Snapshot, status))
			}
		case "status":
			status, e := tx.Status()
			if e != nil {
				return e
			}
			reply = fmt.Sprintf("Monitoramento: última sincronização %s; próximas consulta %s; falhas consecutivas: %d.", formatTime(status.LastSuccess, s.settings.Location), formatTime(status.NextPoll, s.settings.Location), status.Failures)
		case "help":
			reply = "Comandos disponíveis: !start, !agora, !status, !parar e !help."
		default:
			return errors.New("unsupported WhatsApp command")
		}
		if sub.Active {
			return tx.Enqueue(notification.Delivery{Identity: command.Identity, Generation: sub.Generation, Kind: "reply", EventKey: "command:" + command.IncomingID, Revision: "command:" + command.IncomingID, Text: reply, Due: command.ReceivedAt})
		}
		return nil
	})
}

func formatTime(value time.Time, location *time.Location) string {
	if value.IsZero() {
		return "indisponível"
	}
	if location == nil {
		location = time.UTC
	}
	return value.In(location).Format("02/01/2006 15:04:05")
}

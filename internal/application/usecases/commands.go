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
	s.allowedMu.RLock()
	defer s.allowedMu.RUnlock()
	if !s.allowedWhatsApp[command.Identity.Key()] {
		return false, nil
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
				sub, err = tx.SubscriptionByIdentity(command.Identity)
				if err != nil {
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
			source := s.settings.SourceMode
			latest, latestErr := tx.Latest()
			freshness := "sem dados publicados"
			publication := "sem dados publicados"
			if latestErr == nil {
				source = latest.Snapshot.Source
				fresh := s.freshness(latest.Snapshot, status)
				freshness = "sincronização atualizada"
				if fresh.Stale {
					freshness = "sincronização desatualizada"
				}
				publication = "publicação dentro do prazo"
				if fresh.Delayed {
					publication = "publicação atrasada"
				}
			} else if !errors.Is(latestErr, ports.ErrNoSnapshot) {
				return latestErr
			}
			switch source {
			case "simulation":
				source = "simulação local"
			case "official":
				source = "TSE — dados oficiais"
			default:
				source = "indisponível"
			}
			connection, e := tx.ChannelState(subscription.WhatsApp)
			if e != nil {
				return e
			}
			connectionText := "desconhecida"
			switch {
			case connection.AuthenticationRequired:
				connectionText = "autenticação necessária"
			case connection.PausedUntil.After(s.clock.Now()):
				connectionText = "pausada"
			case connection.PauseReason == "connected":
				connectionText = "conectada"
			case connection.PauseReason == "disconnected":
				connectionText = "desconectada"
			}
			reply = fmt.Sprintf("Fonte: %s; %s; %s; conexão WhatsApp: %s; última sincronização: %s; falhas: %d.", source, freshness, publication, connectionText, formatTime(status.LastSuccess, s.settings.Location), status.Failures)
		case "help":
			reply = "Comandos disponíveis: !start, !agora, !status, !parar e !help."
		default:
			return errors.New("unsupported WhatsApp command")
		}
		return tx.Enqueue(notification.Delivery{Identity: command.Identity, Generation: sub.Generation, Kind: "reply", EventKey: "command:" + command.IncomingID, Revision: "command:" + command.IncomingID, Text: reply, Due: command.ReceivedAt})
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

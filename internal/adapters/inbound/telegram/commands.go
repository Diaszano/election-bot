// Package telegram handles Portuguese commands from private Telegram chats.
package telegram

import (
	"context"
	"errors"
	"fmt"
	out "github.com/Diaszano/election-bot/internal/adapters/outbound/telegram"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/application/services"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"strings"
	"time"
)

type Actions interface {
	SubscribeUser(context.Context, int64) error
	UnsubscribeUser(context.Context, int64) error
	GetLatestResults(context.Context) (election.Snapshot, error)
	GetMonitoringStatus(context.Context) (ports.MonitoringStatus, error)
}
type Handler struct {
	actions  Actions
	sender   ports.NotificationSender
	location *time.Location
	username string
}

func New(a Actions, s ports.NotificationSender, l *time.Location, username string) *Handler {
	return &Handler{a, s, l, username}
}

const help = "Comandos disponíveis:\n/start — Receber atualizações\n/agora — Ver resultado atual\n/status — Ver situação do monitoramento\n/parar — Parar atualizações\n/help — Mostrar ajuda"

func (h *Handler) Handle(ctx context.Context, u out.Update) error {
	if u.Message == nil || u.Message.Chat.Type != "private" || u.Message.Chat.ID <= 0 {
		return nil
	}
	m := u.Message
	parts := strings.Fields(m.Text)
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return nil
	}
	command, suffix, has := strings.Cut(parts[0], "@")
	if has && !strings.EqualFold(suffix, h.username) {
		return nil
	}
	var text string
	switch command {
	case "/start":
		if e := h.actions.SubscribeUser(ctx, m.Chat.ID); e != nil {
			return e
		}
		text = "Inscrição ativa! Você receberá resultados e alertas de marcos da apuração."
	case "/parar":
		if e := h.actions.UnsubscribeUser(ctx, m.Chat.ID); e != nil {
			return e
		}
		text = "Atualizações automáticas desativadas. Use /start para voltar."
	case "/agora":
		snapshot, e := h.actions.GetLatestResults(ctx)
		if errors.Is(e, ports.ErrNoSnapshot) {
			text = "Ainda não há resultados disponíveis. Aguardando publicação."
		} else if e != nil {
			return e
		} else {
			status, e := h.actions.GetMonitoringStatus(ctx)
			if e != nil {
				return e
			}
			text = services.FormatResults(snapshot, services.Freshness{Stale: status.Stale, Delayed: status.Delayed, LastSuccess: status.LastSuccess}, h.location)
		}
	case "/status":
		st, e := h.actions.GetMonitoringStatus(ctx)
		if e != nil {
			return e
		}
		last := "nenhuma sincronização bem-sucedida"
		if !st.LastSuccess.IsZero() {
			last = st.LastSuccess.In(h.location).Format("02/01/2006 às 15:04:05")
		}
		text = fmt.Sprintf("📡 Monitoramento ativo\nÚltima sincronização: %s\nFalhas consecutivas: %d", last, st.Failures)
		if st.Stale {
			text += "\n⚠️ Dados possivelmente desatualizados."
		}
	case "/help":
		text = help
	default:
		text = "Comando desconhecido.\n" + help
	}
	_, e := h.sender.Send(ctx, m.Chat.ID, text)
	return e
}

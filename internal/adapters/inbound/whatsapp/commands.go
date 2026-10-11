package whatsapp

import (
	"context"
	"log/slog"
	"strings"
	"time"

	waadapter "github.com/Diaszano/election-bot/internal/adapters/outbound/whatsapp"
	"github.com/Diaszano/election-bot/internal/application/usecases"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
)

type InboundMessage = waadapter.InboundMessage
type IdentityResolver interface {
	Resolve(context.Context, string, string, string) (subscription.Identity, bool, error)
}
type Handler struct {
	actions  *usecases.Service
	resolver IdentityResolver
	allowed  map[string]bool
	logger   *slog.Logger
}

func NewHandler(actions *usecases.Service, resolver IdentityResolver, allowed []string, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	set := make(map[string]bool, len(allowed))
	identity := election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}
	for _, recipient := range allowed {
		if id, err := subscription.NewIdentity(identity, subscription.WhatsApp, recipient); err == nil {
			set[id.Recipient] = true
		}
	}
	return &Handler{actions: actions, resolver: resolver, allowed: set, logger: logger}
}
func (h *Handler) Handle(ctx context.Context, message InboundMessage) error {
	if h == nil || h.actions == nil || h.resolver == nil {
		return nil
	}
	if message.IsFromMe || message.IsGroup || message.IsNewsletter || message.IsHistory || message.IsEdit || message.MessageID == "" {
		return nil
	}
	verb := strings.TrimSpace(message.Text)
	switch verb {
	case "!start":
		verb = "start"
	case "!agora":
		verb = "agora"
	case "!status":
		verb = "status"
	case "!parar":
		verb = "parar"
	case "!help":
		verb = "help"
	default:
		return nil
	}
	identity, verified, err := h.resolver.Resolve(ctx, message.ChatJID, message.SenderJID, message.SenderAltJID)
	if err != nil || !verified {
		h.logger.WarnContext(ctx, "WhatsApp command identity rejected", "operation", "whatsapp.process_command", "reason", "identity_unverified")
		return nil
	}
	if identity.Channel != subscription.WhatsApp || !h.allowed[identity.Recipient] {
		h.logger.InfoContext(ctx, "WhatsApp command ignored", "operation", "whatsapp.process_command", "reason", "not_allowlisted")
		return nil
	}
	received := message.ReceivedAt
	if received.IsZero() {
		received = time.Now()
	}
	_, err = h.actions.ProcessCommand(ctx, notification.Command{Identity: identity, IncomingID: message.MessageID, Verb: verb, ReceivedAt: received})
	if err != nil {
		h.logger.ErrorContext(ctx, "WhatsApp command processing failed", "operation", "whatsapp.process_command", "reason", "application_error")
		return err
	}
	return nil
}

package telegram

import (
	"context"
	"strconv"

	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/notification"
)

// ChannelSender adapts the existing Telegram Bot API client to durable channel attempts.
type ChannelSender struct{ client ports.NotificationSender }

func NewChannelSender(client ports.NotificationSender) ChannelSender {
	return ChannelSender{client: client}
}
func (s ChannelSender) Send(ctx context.Context, recipient string, attempt notification.Attempt) (notification.Acceptance, error) {
	chat, err := strconv.ParseInt(recipient, 10, 64)
	if err != nil || chat <= 0 {
		return notification.Acceptance{}, &ports.ExternalError{Kind: "invalid_chat"}
	}
	id, err := s.client.Send(ctx, chat, attempt.Text)
	if err != nil {
		return notification.Acceptance{}, err
	}
	return notification.Acceptance{Operation: notification.Send, Ref: notification.MessageRef{ID: strconv.FormatInt(id, 10)}}, nil
}
func (s ChannelSender) Edit(ctx context.Context, recipient string, ref notification.MessageRef, attempt notification.Attempt) (notification.Acceptance, error) {
	chat, err := strconv.ParseInt(recipient, 10, 64)
	if err != nil || chat <= 0 {
		return notification.Acceptance{}, &ports.ExternalError{Kind: "invalid_chat"}
	}
	id, err := strconv.ParseInt(ref.ID, 10, 64)
	if err != nil || id <= 0 {
		return notification.Acceptance{}, &ports.ExternalError{Kind: "missing_message"}
	}
	if err = s.client.Edit(ctx, chat, id, attempt.Text); err != nil {
		return notification.Acceptance{}, err
	}
	return notification.Acceptance{Operation: notification.Edit, Ref: ref}, nil
}

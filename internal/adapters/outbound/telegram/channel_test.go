package telegram

import (
	"context"
	"testing"

	"github.com/Diaszano/election-bot/internal/domain/notification"
)

type bridgeClient struct {
	sent   int64
	chat   int64
	edited int64
}

func (c *bridgeClient) Send(_ context.Context, chat int64, _ string) (int64, error) {
	c.chat = chat
	return c.sent, nil
}
func (c *bridgeClient) Edit(_ context.Context, chat, id int64, _ string) error {
	c.chat = chat
	c.edited = id
	return nil
}
func TestChannelSenderUsesPersistedTelegramIdentity(t *testing.T) {
	client := &bridgeClient{sent: 42}
	sender := NewChannelSender(client)
	accepted, err := sender.Send(context.Background(), "123", notification.Attempt{Text: "hello"})
	if err != nil || accepted.Ref.ID != "42" || client.chat != 123 {
		t.Fatalf("accepted=%+v chat=%d err=%v", accepted, client.chat, err)
	}
	_, err = sender.Edit(context.Background(), "123", notification.MessageRef{ID: "42"}, notification.Attempt{Text: "updated"})
	if err != nil || client.edited != 42 {
		t.Fatalf("edited=%d err=%v", client.edited, err)
	}
}

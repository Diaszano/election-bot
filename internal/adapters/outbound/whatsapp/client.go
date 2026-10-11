package whatsapp

import (
	"context"
	"errors"
	"time"

	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	wa "go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

type messageAPI interface {
	SendMessage(context.Context, types.JID, *waE2E.Message, ...wa.SendRequestExtra) (wa.SendResponse, error)
	BuildEdit(types.JID, types.MessageID, *waE2E.Message) *waE2E.Message
}

// Client adapts whatsmeow to the application's durable channel sender and connection contract.
type Client struct {
	api         messageAPI
	sdk         *wa.Client
	session     *SessionStore
	sendTimeout time.Duration
	events      chan ConnectionEvent
	handlerID   uint32
}

func NewClient(session *SessionStore, sendTimeout time.Duration) *Client {
	sdk := wa.NewClient(session.Device(), waLog.Noop)
	sdk.EnableAutoReconnect = false
	c := &Client{api: sdk, sdk: sdk, session: session, sendTimeout: sendTimeout, events: make(chan ConnectionEvent, 32)}
	c.handlerID = sdk.AddEventHandler(c.handleEvent)
	return c
}
func (c *Client) handleEvent(raw any) {
	var event ConnectionEvent
	switch evt := raw.(type) {
	case *events.Connected:
		event.Kind = ConnectionConnected
	case *events.Disconnected:
		event.Kind = ConnectionDisconnected
	case *events.LoggedOut:
		event.Kind = ConnectionAuthenticationRequired
	case *events.TemporaryBan:
		event.Kind = ConnectionRestricted
		event.Delay = evt.Expire
	case *events.ClientOutdated:
		event.Kind = ConnectionOutdated
	case *events.StreamReplaced:
		event.Kind = ConnectionRestricted
		event.Reason = "stream_replaced"
	default:
		return
	}
	select {
	case c.events <- event:
	default:
	}
}
func (c *Client) Paired() bool {
	return c.session != nil && c.session.Device() != nil && c.session.Device().ID != nil
}
func (c *Client) Connect(ctx context.Context) error { return c.sdk.ConnectContext(ctx) }
func (c *Client) Disconnect() {
	if c.sdk != nil {
		c.sdk.Disconnect()
	}
}
func (c *Client) Events() <-chan ConnectionEvent { return c.events }
func (c *Client) Close() error {
	if c.sdk != nil {
		c.sdk.RemoveEventHandler(c.handlerID)
		c.sdk.Disconnect()
	}
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}

func (c *Client) Send(ctx context.Context, recipient string, attempt notification.Attempt) (notification.Acceptance, error) {
	return c.send(ctx, recipient, attempt, nil)
}
func (c *Client) Edit(ctx context.Context, recipient string, ref notification.MessageRef, attempt notification.Attempt) (notification.Acceptance, error) {
	if attempt.TargetMessageID == "" || attempt.TargetMessageID != ref.ID {
		return notification.Acceptance{}, &ports.ExternalError{Kind: "payload"}
	}
	return c.send(ctx, recipient, attempt, &ref)
}
func (c *Client) send(ctx context.Context, recipient string, attempt notification.Attempt, edit *notification.MessageRef) (notification.Acceptance, error) {
	jid, err := parseRecipient(recipient)
	if err != nil {
		return notification.Acceptance{}, &ports.ExternalError{Kind: "invalid_chat"}
	}
	if attempt.RequestID == "" || attempt.Text == "" {
		return notification.Acceptance{}, &ports.ExternalError{Kind: "payload"}
	}
	operationCtx := ctx
	cancel := func() {}
	if c.sendTimeout > 0 {
		operationCtx, cancel = context.WithTimeout(ctx, c.sendTimeout)
	}
	defer cancel()
	message := &waE2E.Message{Conversation: proto.String(attempt.Text)}
	if edit != nil {
		if edit.ID == "" || attempt.TargetMessageID != edit.ID {
			return notification.Acceptance{}, &ports.ExternalError{Kind: "missing_message"}
		}
		message = c.api.BuildEdit(jid, types.MessageID(attempt.TargetMessageID), message)
	}
	response, err := c.api.SendMessage(operationCtx, jid, message, wa.SendRequestExtra{ID: types.MessageID(attempt.RequestID), Timeout: c.sendTimeout})
	if err != nil {
		if operationCtx.Err() != nil {
			return notification.Acceptance{}, operationCtx.Err()
		}
		return notification.Acceptance{}, &ports.ExternalError{Kind: "transient"}
	}
	ref := notification.MessageRef{ID: string(response.ID), OriginalSentAt: response.Timestamp}
	if edit != nil {
		ref = *edit
	}
	operation := notification.Send
	if edit != nil {
		operation = notification.Edit
	}
	return notification.Acceptance{Ref: ref, Operation: operation, AcceptedAt: response.Timestamp}, nil
}
func parseRecipient(value string) (types.JID, error) {
	jid, err := types.ParseJID(value)
	if err != nil || jid.Server != "s.whatsapp.net" || len(jid.User) < 8 || len(jid.User) > 15 || jid.Device != 0 || jid.RawAgent != 0 {
		return types.EmptyJID, errors.New("invalid WhatsApp recipient")
	}
	for _, r := range jid.User {
		if r < '0' || r > '9' {
			return types.EmptyJID, errors.New("invalid WhatsApp recipient")
		}
	}
	return jid, nil
}

var _ ports.ChannelSender = (*Client)(nil)

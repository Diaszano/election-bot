package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Diaszano/election-bot/internal/domain/notification"
	wa "go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

type fakeWAAPI struct {
	id         types.MessageID
	chat       types.JID
	content    *waE2E.Message
	extra      wa.SendRequestExtra
	editTarget types.MessageID
	block      bool
}

func (f *fakeWAAPI) SendMessage(ctx context.Context, to types.JID, msg *waE2E.Message, extra ...wa.SendRequestExtra) (wa.SendResponse, error) {
	if f.block {
		<-ctx.Done()
		return wa.SendResponse{}, ctx.Err()
	}
	f.chat = to
	f.content = msg
	if len(extra) > 0 {
		f.extra = extra[0]
	}
	return wa.SendResponse{ID: f.id, Timestamp: time.Now()}, nil
}
func (f *fakeWAAPI) BuildEdit(_ types.JID, id types.MessageID, msg *waE2E.Message) *waE2E.Message {
	f.editTarget = id
	return msg
}

func TestStableRequestIDSent(t *testing.T) {
	api := &fakeWAAPI{id: types.MessageID("wa-accepted")}
	client := &Client{api: api, sendTimeout: time.Second}
	attempt := notification.Attempt{RequestID: "stable-request-id", Text: "apuração"}
	accepted, err := client.Send(context.Background(), "5511999999999@s.whatsapp.net", attempt)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(api.extra.ID) != "stable-request-id" || accepted.Ref.ID != "wa-accepted" {
		t.Fatalf("request=%s acceptance=%+v", api.extra.ID, accepted)
	}
}
func TestSendEditAcceptance(t *testing.T) {
	api := &fakeWAAPI{id: types.MessageID("updated")}
	client := &Client{api: api, sendTimeout: time.Second}
	attempt := notification.Attempt{RequestID: "edit-request", Text: "novo texto", TargetMessageID: "original"}
	accepted, err := client.Edit(context.Background(), "5511999999999@s.whatsapp.net", notification.MessageRef{ID: "original"}, attempt)
	if err != nil {
		t.Fatal(err)
	}
	if api.editTarget != "original" || accepted.Operation != notification.Edit || accepted.Ref.ID != "original" {
		t.Fatalf("target=%s acceptance=%+v", api.editTarget, accepted)
	}
}
func TestSendTimeoutAndCancellation(t *testing.T) {
	api := &fakeWAAPI{block: true}
	client := &Client{api: api, sendTimeout: 20 * time.Millisecond}
	_, err := client.Send(context.Background(), "5511999999999@s.whatsapp.net", notification.Attempt{RequestID: "id", Text: "x"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected whole-operation timeout, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Send(ctx, "5511999999999@s.whatsapp.net", notification.Attempt{RequestID: "id", Text: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

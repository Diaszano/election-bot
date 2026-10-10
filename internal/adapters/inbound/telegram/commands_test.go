package telegram

import (
	"context"
	out "github.com/Diaszano/election-bot/internal/adapters/outbound/telegram"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"testing"
	"time"
)

type actions struct{ starts, stops int }

func (a *actions) SubscribeUser(context.Context, int64) error   { a.starts++; return nil }
func (a *actions) UnsubscribeUser(context.Context, int64) error { a.stops++; return nil }
func (a *actions) GetLatestResults(context.Context) (election.Snapshot, error) {
	return election.Snapshot{}, ports.ErrNoSnapshot
}
func (a *actions) GetMonitoringStatus(context.Context) (ports.MonitoringStatus, error) {
	return ports.MonitoringStatus{}, nil
}

type sender struct{ calls int }

func (s *sender) Send(context.Context, int64, string) (int64, error) { s.calls++; return 1, nil }
func (s *sender) Edit(context.Context, int64, int64, string) error   { return nil }
func TestCommandsPrivateOnlyAndSuffix(t *testing.T) {
	a := &actions{}
	s := &sender{}
	h := New(a, s, time.UTC, "election_bot")
	ctx := context.Background()
	for _, kind := range []string{"group", "supergroup", "channel"} {
		if e := h.Handle(ctx, out.Update{Message: &out.Message{Chat: out.Chat{ID: 1, Type: kind}, Text: "/start"}}); e != nil {
			t.Fatal(e)
		}
	}
	if a.starts != 0 || s.calls != 0 {
		t.Fatal("non-private subscription")
	}
	for _, cmd := range []string{"/start@election_bot", "/agora", "/status", "/parar", "/help"} {
		if e := h.Handle(ctx, out.Update{Message: &out.Message{Chat: out.Chat{ID: 1, Type: "private"}, Text: cmd}}); e != nil {
			t.Fatal(e)
		}
	}
	if a.starts != 1 || a.stops != 1 || s.calls != 5 {
		t.Fatal(a, s)
	}
	h.Handle(ctx, out.Update{Message: &out.Message{Chat: out.Chat{ID: 1, Type: "private"}, Text: "/start@other"}})
	if a.starts != 1 {
		t.Fatal("wrong suffix")
	}
}

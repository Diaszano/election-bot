package telegram

import (
	"context"
	out "github.com/Diaszano/election-bot/internal/adapters/outbound/telegram"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"strings"
	"testing"
	"time"
)

type actions struct {
	starts, stops int
	status        ports.MonitoringStatus
}

func (a *actions) SubscribeUser(context.Context, int64) error   { a.starts++; return nil }
func (a *actions) UnsubscribeUser(context.Context, int64) error { a.stops++; return nil }
func (a *actions) GetLatestResults(context.Context) (election.Snapshot, error) {
	return election.Snapshot{}, ports.ErrNoSnapshot
}
func (a *actions) GetMonitoringStatus(context.Context) (ports.MonitoringStatus, error) {
	return a.status, nil
}

type sender struct {
	calls int
	err   error
	last  string
}

func (s *sender) Send(_ context.Context, _ int64, text string) (int64, error) {
	s.calls++
	s.last = text
	return 1, s.err
}
func (s *sender) Edit(context.Context, int64, int64, string) error { return nil }
func TestCommandsPrivateOnlyAndSuffix(t *testing.T) {
	a := &actions{}
	s := &sender{}
	h := New(a, s, time.UTC, "election_bot", "simulation")
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

func TestPermanentReplyFailureDoesNotPoisonCommands(t *testing.T) {
	for _, kind := range []string{"blocked", "invalid_chat", "payload", "transient"} {
		t.Run(kind, func(t *testing.T) {
			a := &actions{}
			s := &sender{err: &ports.ExternalError{Kind: kind}}
			h := New(a, s, time.UTC, "bot", "simulation")
			err := h.Handle(context.Background(), out.Update{Message: &out.Message{Chat: out.Chat{ID: 1, Type: "private"}, Text: "/help"}})
			if kind == "transient" {
				if err == nil {
					t.Fatal("transient reply must retry")
				}
				return
			}
			if err != nil {
				t.Fatal("permanent reply blocks offset", err)
			}
			if (kind == "blocked" || kind == "invalid_chat") && a.stops != 1 {
				t.Fatal("subscription remains active")
			}
			s.err = nil
			if err = h.Handle(context.Background(), out.Update{Message: &out.Message{Chat: out.Chat{ID: 2, Type: "private"}, Text: "/start"}}); err != nil {
				t.Fatal(err)
			}
			if a.starts != 1 {
				t.Fatal("next user blocked")
			}
		})
	}
}

func TestStatusShowsSourceSynchronizationAndPublicationFreshness(t *testing.T) {
	now := time.Date(2026, 10, 25, 20, 0, 0, 0, time.FixedZone("BRT", -3*60*60))
	cases := []struct {
		name, mode string
		status     ports.MonitoringStatus
		want       []string
	}{
		{"no publication", "simulation", ports.MonitoringStatus{}, []string{"Fonte de dados: simulação", "Sincronização: nunca", "Publicação TSE: aguardando dados"}},
		{"fresh", "official", ports.MonitoringStatus{LastSuccess: now, PublishedAt: now.Add(-time.Minute)}, []string{"Fonte de dados: oficial", "Sincronização: atualizada", "Publicação TSE: atualizada", "25/10/2026"}},
		{"stale and delayed", "official", ports.MonitoringStatus{LastSuccess: now.Add(-10 * time.Minute), PublishedAt: now.Add(-20 * time.Minute), Stale: true, Delayed: true}, []string{"Fonte de dados: oficial", "Sincronização: desatualizada", "Publicação TSE: atrasada", "25/10/2026"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &actions{status: tc.status}
			s := &sender{}
			h := New(a, s, time.UTC, "bot", tc.mode)
			if err := h.Handle(context.Background(), out.Update{Message: &out.Message{Chat: out.Chat{ID: 1, Type: "private"}, Text: "/status"}}); err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(s.last, want) {
					t.Errorf("status %q missing %q", s.last, want)
				}
			}
		})
	}
}

package whatsapp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
)

type fakeConnector struct {
	mu           sync.Mutex
	paired       bool
	connectErr   error
	events       chan ConnectionEvent
	connects     int
	disconnected bool
}

func (f *fakeConnector) Paired() bool { return f.paired }
func (f *fakeConnector) Connect(context.Context) error {
	f.mu.Lock()
	f.connects++
	f.mu.Unlock()
	return f.connectErr
}
func (f *fakeConnector) Disconnect()                    { f.mu.Lock(); f.disconnected = true; f.mu.Unlock() }
func (f *fakeConnector) Events() <-chan ConnectionEvent { return f.events }

type fakeChannelState struct {
	mu    sync.Mutex
	state notification.ChannelState
}

func (f *fakeChannelState) ReadChannelState(context.Context, subscription.Channel) (notification.ChannelState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}
func (f *fakeChannelState) SaveChannelState(_ context.Context, _ subscription.Channel, s notification.ChannelState) error {
	f.mu.Lock()
	f.state = s
	f.mu.Unlock()
	return nil
}

func waitState(t *testing.T, changes <-chan ConnectionState, want ConnectionState) {
	t.Helper()
	select {
	case got := <-changes:
		if got != want {
			t.Fatalf("state=%s want=%s", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("state %s not reached", want)
	}
}
func TestConnectionTransitions(t *testing.T) {
	conn := &fakeConnector{paired: true, events: make(chan ConnectionEvent, 4)}
	store := &fakeChannelState{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := NewConnectionManager(conn, store, time.Now, ConnectionSettings{Enabled: true, ReconnectEnabled: true, ConnectTimeout: time.Second}, nil)
	changes := m.Changes()
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	waitState(t, changes, StateConnecting)
	conn.events <- ConnectionEvent{Kind: ConnectionConnected}
	waitState(t, changes, StateConnected)
	conn.events <- ConnectionEvent{Kind: ConnectionDisconnected}
	waitState(t, changes, StateReconnecting)
	conn.events <- ConnectionEvent{Kind: ConnectionConnected}
	waitState(t, changes, StateConnected)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not stop manager")
	}
	if !conn.disconnected {
		t.Fatal("shutdown did not disconnect")
	}
}
func TestRevocationStopsReconnect(t *testing.T) {
	conn := &fakeConnector{paired: true, events: make(chan ConnectionEvent, 1)}
	store := &fakeChannelState{}
	m := NewConnectionManager(conn, store, time.Now, ConnectionSettings{Enabled: true, ReconnectEnabled: true, ConnectTimeout: time.Second}, nil)
	done := make(chan error, 1)
	go func() { done <- m.Run(context.Background()) }()
	conn.events <- ConnectionEvent{Kind: ConnectionAuthenticationRequired}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("revocation did not stop reconnect")
	}
	if m.State() != StateAuthenticationRequired {
		t.Fatal(m.State())
	}
	if !store.state.AuthenticationRequired {
		t.Fatal("authentication revocation was not persisted")
	}
	conn.mu.Lock()
	n := conn.connects
	conn.mu.Unlock()
	if n != 1 {
		t.Fatalf("reconnected after revocation: %d", n)
	}
}
func TestRestrictionPauseSurvivesRestart(t *testing.T) {
	conn := &fakeConnector{paired: true, events: make(chan ConnectionEvent, 1)}
	store := &fakeChannelState{}
	now := time.Now()
	waited := make(chan time.Duration, 1)
	wait := func(ctx context.Context, d time.Duration) error { waited <- d; <-ctx.Done(); return ctx.Err() }
	m := NewConnectionManager(conn, store, func() time.Time { return now }, ConnectionSettings{Enabled: true, ReconnectEnabled: true, ConnectTimeout: time.Second, ReconnectCooldown: time.Minute}, wait)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	waitState(t, m.Changes(), StateConnecting)
	conn.events <- ConnectionEvent{Kind: ConnectionConnected}
	waitState(t, m.Changes(), StateConnected)
	conn.events <- ConnectionEvent{Kind: ConnectionRestricted}
	select {
	case d := <-waited:
		if d != time.Minute {
			t.Fatalf("pause=%s", d)
		}
	case <-time.After(time.Second):
		t.Fatal("restriction pause was not persisted")
	}
	cancel()
	<-done
	if !store.state.PausedUntil.Equal(now.Add(time.Minute)) {
		t.Fatal("restriction pause did not survive worker shutdown")
	}
	conn2 := &fakeConnector{paired: true, events: make(chan ConnectionEvent)}
	waited2 := make(chan time.Duration, 1)
	m2 := NewConnectionManager(conn2, store, func() time.Time { return now }, ConnectionSettings{Enabled: true, ReconnectEnabled: true, ConnectTimeout: time.Second, ReconnectCooldown: time.Minute}, func(ctx context.Context, d time.Duration) error { waited2 <- d; <-ctx.Done(); return ctx.Err() })
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- m2.Run(ctx2) }()
	select {
	case d := <-waited2:
		if d != time.Minute {
			t.Fatalf("restored pause=%s", d)
		}
	case <-time.After(time.Second):
		t.Fatal("restart ignored persisted pause")
	}
	cancel2()
	<-done2
}
func TestBoundedReconnectCancellation(t *testing.T) {
	conn := &fakeConnector{paired: true, connectErr: errors.New("private raw transport error"), events: make(chan ConnectionEvent)}
	store := &fakeChannelState{}
	ctx, cancel := context.WithCancel(context.Background())
	waits := 0
	wait := func(ctx context.Context, _ time.Duration) error {
		waits++
		if waits == 3 {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	m := NewConnectionManager(conn, store, time.Now, ConnectionSettings{Enabled: true, ReconnectEnabled: true, ConnectTimeout: time.Second, ReconnectCooldown: time.Minute}, wait)
	if err := m.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	conn.mu.Lock()
	connects := conn.connects
	conn.mu.Unlock()
	if connects != 3 || waits != 3 {
		t.Fatalf("connect attempts=%d waits=%d", connects, waits)
	}
}

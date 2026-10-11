package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
)

type ConnectionState string

const (
	StateDisabled               ConnectionState = "disabled"
	StateNotPaired              ConnectionState = "not_paired"
	StateConnecting             ConnectionState = "connecting"
	StateConnected              ConnectionState = "connected"
	StateReconnecting           ConnectionState = "reconnecting"
	StateDisconnected           ConnectionState = "disconnected"
	StateAuthenticationRequired ConnectionState = "authentication_required"
)

type ConnectionEventKind string

const (
	ConnectionConnected              ConnectionEventKind = "connected"
	ConnectionDisconnected           ConnectionEventKind = "disconnected"
	ConnectionAuthenticationRequired ConnectionEventKind = "authentication_required"
	ConnectionRestricted             ConnectionEventKind = "restricted"
	ConnectionOutdated               ConnectionEventKind = "outdated"
)

type ConnectionEvent struct {
	Kind   ConnectionEventKind
	Delay  time.Duration
	Reason string
}

type Connection interface {
	Paired() bool
	Connect(context.Context) error
	Disconnect()
	Events() <-chan ConnectionEvent
}
type WaitFunc func(context.Context, time.Duration) error
type ConnectionSettings struct {
	Enabled, ReconnectEnabled         bool
	ConnectTimeout, ReconnectCooldown time.Duration
}
type ConnectionManager struct {
	connection Connection
	stateStore ports.ChannelStateStore
	now        func() time.Time
	settings   ConnectionSettings
	wait       WaitFunc
	mu         sync.RWMutex
	state      ConnectionState
	changes    chan ConnectionState
}

func NewConnectionManager(connection Connection, stateStore ports.ChannelStateStore, now func() time.Time, settings ConnectionSettings, wait WaitFunc) *ConnectionManager {
	if now == nil {
		now = time.Now
	}
	if settings.ConnectTimeout <= 0 {
		settings.ConnectTimeout = 30 * time.Second
	}
	if settings.ReconnectCooldown <= 0 {
		settings.ReconnectCooldown = time.Minute
	}
	if wait == nil {
		wait = waitContext
	}
	return &ConnectionManager{connection: connection, stateStore: stateStore, now: now, settings: settings, wait: wait, state: StateDisabled, changes: make(chan ConnectionState, 16)}
}
func (m *ConnectionManager) State() ConnectionState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}
func (m *ConnectionManager) Changes() <-chan ConnectionState { return m.changes }
func (m *ConnectionManager) setState(state ConnectionState) {
	m.mu.Lock()
	changed := m.state != state
	m.state = state
	m.mu.Unlock()
	if changed {
		select {
		case m.changes <- state:
		default:
		}
		slog.Info("WhatsApp connection state changed", "state", state)
	}
}

// Run owns reconnect decisions while the SDK's unbounded auto-reconnect is disabled.
func (m *ConnectionManager) Run(ctx context.Context) error {
	if !m.settings.Enabled {
		m.setState(StateDisabled)
		return nil
	}
	if m.connection == nil || !m.connection.Paired() {
		m.setState(StateNotPaired)
		return nil
	}
	defer m.connection.Disconnect()
	state, err := m.stateStore.ReadChannelState(ctx, subscription.WhatsApp)
	if err != nil {
		return err
	}
	if state.AuthenticationRequired {
		m.setState(StateAuthenticationRequired)
		return nil
	}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		if state.PausedUntil.After(m.now()) {
			m.setState(StateDisconnected)
			if err = m.wait(ctx, state.PausedUntil.Sub(m.now())); err != nil {
				return err
			}
			state.PausedUntil = time.Time{}
			state.PauseReason = ""
			if err = m.stateStore.SaveChannelState(ctx, subscription.WhatsApp, state); err != nil {
				return err
			}
		}
		for attempt := 0; attempt < 3; attempt++ {
			if attempt == 0 {
				m.setState(StateConnecting)
			} else {
				m.setState(StateReconnecting)
			}
			connectCtx, cancel := context.WithTimeout(ctx, m.settings.ConnectTimeout)
			err = m.connection.Connect(connectCtx)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				var fatal *ConnectionError
				if errors.As(err, &fatal) && fatal.Kind == "authentication_required" {
					return m.requireAuthentication(ctx)
				}
				if !m.settings.ReconnectEnabled || attempt == 2 {
					break
				}
				if err = m.wait(ctx, reconnectDelay(attempt)); err != nil {
					return err
				}
				continue
			}
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case event, ok := <-m.connection.Events():
					if !ok {
						break
					}
					switch event.Kind {
					case ConnectionConnected:
						m.setState(StateConnected)
						state.AuthenticationRequired = false
						state.PausedUntil = time.Time{}
						state.PauseReason = "connected"
						if err = m.stateStore.SaveChannelState(ctx, subscription.WhatsApp, state); err != nil {
							return err
						}
						continue
					case ConnectionDisconnected:
						m.setState(StateReconnecting)
						state.PauseReason = "disconnected"
						if err = m.stateStore.SaveChannelState(ctx, subscription.WhatsApp, state); err != nil {
							return err
						}
						break
					case ConnectionAuthenticationRequired:
						return m.requireAuthentication(ctx)
					case ConnectionRestricted, ConnectionOutdated:
						delay := event.Delay
						if event.Kind == ConnectionOutdated {
							delay = 24 * time.Hour
						}
						if delay <= 0 {
							delay = m.settings.ReconnectCooldown
						}
						state.PausedUntil = m.now().Add(delay)
						state.PauseReason = "restricted"
						if event.Kind == ConnectionOutdated {
							state.PauseReason = "client_outdated"
						}
						if err = m.stateStore.SaveChannelState(ctx, subscription.WhatsApp, state); err != nil {
							return err
						}
						m.setState(StateDisconnected)
						break
					}
				}
				if !state.PausedUntil.IsZero() || m.State() == StateReconnecting {
					break
				}
			}
			if state.PausedUntil.After(m.now()) {
				break
			}
			if !m.settings.ReconnectEnabled {
				m.setState(StateDisconnected)
				return nil
			}
			continue
		}
		if !m.settings.ReconnectEnabled {
			m.setState(StateDisconnected)
			return nil
		}
		if state.PausedUntil.IsZero() {
			state.PausedUntil = m.now().Add(m.settings.ReconnectCooldown)
			state.PauseReason = "transient"
			if err = m.stateStore.SaveChannelState(ctx, subscription.WhatsApp, state); err != nil {
				return err
			}
		}
	}
}

func (m *ConnectionManager) requireAuthentication(ctx context.Context) error {
	m.setState(StateAuthenticationRequired)
	state, err := m.stateStore.ReadChannelState(ctx, subscription.WhatsApp)
	if err != nil {
		return err
	}
	state.AuthenticationRequired = true
	state.PauseReason = "authentication_required"
	if err = m.stateStore.SaveChannelState(ctx, subscription.WhatsApp, state); err != nil {
		return err
	}
	return nil
}
func waitContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(max(d, 0))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func reconnectDelay(attempt int) time.Duration {
	base := min(time.Duration(1<<min(attempt, 8))*time.Second, 5*time.Minute)
	return base/2 + time.Duration(rand.Int64N(int64(max(base/2, time.Second))))
}

type ConnectionError struct{ Kind string }

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("WhatsApp connection failed: %s", e.Kind)
}

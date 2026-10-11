// Package lifecycle composes local components and coordinates graceful shutdown.
package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/Diaszano/election-bot/internal/adapters/inbound/health"
	"github.com/Diaszano/election-bot/internal/adapters/inbound/scheduler"
	commands "github.com/Diaszano/election-bot/internal/adapters/inbound/telegram"
	waCommands "github.com/Diaszano/election-bot/internal/adapters/inbound/whatsapp"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/simulation"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/sqlite"
	telegramapi "github.com/Diaszano/election-bot/internal/adapters/outbound/telegram"
	waadapter "github.com/Diaszano/election-bot/internal/adapters/outbound/whatsapp"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/application/services"
	"github.com/Diaszano/election-bot/internal/application/usecases"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
	"github.com/Diaszano/election-bot/internal/platform/config"
	"github.com/Diaszano/election-bot/internal/platform/observability"
	"github.com/Diaszano/election-bot/internal/platform/version"
	"net/http"
	"sync"
	"time"
)

func Run(parent context.Context, c config.Config) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	vInfo := version.Get()
	telemetry, e := observability.Setup(ctx, c, vInfo.Version)
	if e != nil {
		return e
	}
	logger := telemetry.Logger
	db, e := sqlite.Open(ctx, c.Database)
	if e != nil {
		return e
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
		defer cancel()
		if e := telemetry.Shutdown(shutdown); e != nil {
			logger.Warn("telemetry shutdown incomplete")
		}
		db.Close()
	}()

	if e = observability.RegisterState(func(ctx context.Context) (v observability.Values, e error) {
		status, e := db.Status(ctx)
		if e != nil {
			return v, e
		}
		v.LastSuccess = status.LastSuccess
		v.Failures = status.Failures
		snapshot, e := db.Latest(ctx)
		if e != nil && !errors.Is(e, ports.ErrNoSnapshot) {
			return v, e
		}
		v.Published = snapshot.Snapshot.Published
		v.Progress = snapshot.Snapshot.Progress.Ratio()
		v.ValidVotes = snapshot.Snapshot.Valid
		v.Counting = snapshot.Snapshot.Status == election.Counting
		v.Subscribers, v.Pending, v.OldestPending, e = db.Statistics(ctx)
		if e == nil && c.WhatsAppEnabled && c.WhatsAppTelemetryEnabled {
			var last time.Time
			v.WhatsAppSubscribers, v.WhatsAppPending, v.WhatsAppFailed, last, e = db.ChannelStatistics(ctx, subscription.WhatsApp)
			if e == nil {
				v.WhatsAppLastSuccess = last
				state, stateErr := db.ReadChannelState(ctx, subscription.WhatsApp)
				e = stateErr
				v.WhatsAppConnectionState = observability.WhatsAppConnectionStateValue(state.AuthenticationRequired, state.PauseReason)
			}
		}
		return v, e
	}, c.WhatsAppEnabled && c.WhatsAppTelemetryEnabled); e != nil {
		logger.Warn("state metrics initialization failed")
	}

	var sender ports.NotificationSender
	var api *telegramapi.Client
	username := ""
	if c.TelegramEnabled {
		api = telegramapi.New(&http.Client{Timeout: time.Duration(c.PollTimeout+5) * time.Second}, "https://api.telegram.org", c.Token, time.Second)
		identity, e := api.GetMe(ctx)
		if e != nil {
			return e
		}
		username = identity.Username
		sender = api
	}
	settings := usecases.Settings{Location: c.Location, PollInterval: c.PollInterval, BeforeInterval: c.BeforeInterval, FinalInterval: c.FinalInterval, StaleAfter: c.StaleAfter, DelayedAfter: c.DelayedAfter, CountingStart: c.CountingStart, SourceMode: c.Mode, AllowedWhatsAppRecipients: c.WhatsAppAllowedRecipients}
	if c.Mode == "simulation" {
		settings.CountingStart = time.Time{}
	}
	svc := usecases.New(db, db, simulation.New(), sender, ports.RealClock{}, settings)
	var wg sync.WaitGroup
	start := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }
	start(func() { scheduler.Run(ctx, time.Second, "election.poll", logger, svc.PollElectionResults) })
	if c.TelegramEnabled {
		publisher := usecases.NewPublisher(subscription.Telegram, db, telegramapi.NewChannelSender(api), ports.RealClock{}, notification.Policy{})
		start(func() {
			scheduler.Run(ctx, c.NotificationInterval, "notifications.publish.telegram", logger, func(ctx context.Context) error { return publisher.Publish(ctx) })
		})
	}
	var waClient *waadapter.Client
	if c.WhatsAppEnabled {
		if e = svc.ReconcileAllowedRecipients(ctx, c.WhatsAppAllowedRecipients); e != nil {
			logger.Warn("WhatsApp recipient reconciliation failed", "reason", "store_unavailable")
		} else if session, openErr := waadapter.OpenSession(ctx, c.WhatsAppSessionDB); openErr != nil {
			logger.Warn("WhatsApp session unavailable; delivery suspended", "reason", "session_unavailable")
		} else {
			waClient = waadapter.NewClient(session, c.HTTPTimeout)
			accountReady := false
			state, stateErr := db.ReadChannelState(ctx, subscription.WhatsApp)
			if stateErr == nil && !waClient.Paired() {
				state.AuthenticationRequired = true
				state.PauseReason = "not_paired"
				stateErr = db.SaveChannelState(ctx, subscription.WhatsApp, state)
			} else if stateErr == nil && waClient.Paired() {
				digest := sha256.Sum256([]byte(session.Device().GetJID().String()))
				accountReady = state.AccountBinding != "" && state.AccountBinding == hex.EncodeToString(digest[:]) && !state.AuthenticationRequired
				if !accountReady {
					state.AuthenticationRequired = true
					if state.AccountBinding == "" {
						state.PauseReason = "account_unbound"
					} else if state.AccountBinding != hex.EncodeToString(digest[:]) {
						state.PauseReason = "account_changed"
					} else if state.PauseReason == "" {
						state.PauseReason = "authentication_required"
					}
				} else {
					state.PauseReason = "connecting"
				}
				stateErr = db.SaveChannelState(ctx, subscription.WhatsApp, state)
			}
			if stateErr != nil {
				logger.Warn("WhatsApp state unavailable; delivery suspended", "reason", "state_store_unavailable")
				_ = waClient.Close()
				waClient = nil
			} else {
				waPublisher := usecases.NewPublisher(subscription.WhatsApp, db, waClient, ports.RealClock{}, notification.Policy{EditMin: c.WhatsAppEditMinInterval, Digest: c.WhatsAppDigestInterval, RecipientMin: c.WhatsAppRecipientMinInterval, GlobalMin: time.Second}, usecases.WithTelemetry(c.WhatsAppTelemetryEnabled))
				start(func() {
					scheduler.Run(ctx, c.NotificationInterval, "notifications.publish.whatsapp", logger, func(ctx context.Context) error { return waPublisher.Publish(ctx) })
				})
				manager := waadapter.NewConnectionManager(waClient, db, time.Now, waadapter.ConnectionSettings{Enabled: true, ReconnectEnabled: c.WhatsAppReconnectEnabled, TelemetryEnabled: c.WhatsAppTelemetryEnabled, ConnectTimeout: c.WhatsAppConnectTimeout}, nil)
				start(func() {
					if err := manager.Run(ctx); err != nil && ctx.Err() == nil {
						logger.Warn("WhatsApp connection manager stopped", "reason", "connection_unavailable")
					}
				})
				if c.WhatsAppTelemetryEnabled {
					start(func() {
						for {
							select {
							case <-ctx.Done():
								return
							case state := <-manager.Changes():
								if state == waadapter.StateReconnecting {
									services.Counter(ctx, "whatsapp_reconnects")
								}
							}
						}
					})
				}
				if accountReady {
					resolver := waadapter.NewIdentityResolver(session.Device(), election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1})
					allowed := make([]string, 0, len(c.WhatsAppAllowedRecipients))
					for _, identity := range c.WhatsAppAllowedRecipients {
						allowed = append(allowed, identity.Recipient)
					}
					handler := waCommands.NewHandler(svc, resolver, allowed, logger, c.WhatsAppTelemetryEnabled)
					start(func() {
						for {
							select {
							case <-ctx.Done():
								return
							case message := <-waClient.Messages():
								if err := handler.Handle(ctx, message); err != nil {
									logger.WarnContext(ctx, "WhatsApp command was not processed", "reason", "command_processing_failed")
								}
							}
						}
					})
				}
				defer func() {
					if err := waClient.Close(); err != nil {
						logger.Warn("WhatsApp session close incomplete", "reason", "session_close_failed")
					}
				}()
			}
		}
	}
	if api != nil {
		h := commands.New(svc, sender, c.Location, username, c.Mode)
		start(func() {
			scheduler.Run(ctx, time.Second, "telegram.commands", logger, func(ctx context.Context) error {
				offset, e := db.Offset(ctx)
				if e != nil {
					return e
				}
				updates, e := api.GetUpdates(ctx, offset, c.PollTimeout)
				if e != nil {
					return e
				}
				for _, u := range updates {
					if u.ID < offset {
						continue
					}
					if e = h.Handle(ctx, u); e != nil {
						return e
					}
					if e = db.SetOffset(ctx, u.ID+1); e != nil {
						return e
					}
					offset = u.ID + 1
				}
				return nil
			})
		})
	}
	server := &http.Server{Addr: c.Address, Handler: health.New(db.Ping), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: time.Minute}
	errs := make(chan error, 1)
	start(func() {
		e := server.ListenAndServe()
		if e != nil && !errors.Is(e, http.ErrServerClosed) {
			errs <- e
			cancel()
		}
	})
	logger.Info("election-bot initialized",
		"version", vInfo.Version,
		"commit", vInfo.Commit,
		"source_mode", c.Mode,
		"telegram_enabled", c.TelegramEnabled,
	)
	<-ctx.Done()
	shutdown, stop := context.WithTimeout(context.Background(), c.ShutdownTimeout)
	defer stop()
	e = server.Shutdown(shutdown)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-shutdown.Done():
		e = errors.Join(e, shutdown.Err())
	}
	select {
	case x := <-errs:
		e = errors.Join(e, x)
	default:
	}
	logger.Info("election-bot stopped")
	return e
}

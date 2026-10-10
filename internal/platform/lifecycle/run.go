// Package lifecycle composes local components and coordinates graceful shutdown.
package lifecycle

import (
	"context"
	"errors"
	"github.com/Diaszano/election-bot/internal/adapters/inbound/health"
	"github.com/Diaszano/election-bot/internal/adapters/inbound/scheduler"
	commands "github.com/Diaszano/election-bot/internal/adapters/inbound/telegram"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/simulation"
	"github.com/Diaszano/election-bot/internal/adapters/outbound/sqlite"
	telegramapi "github.com/Diaszano/election-bot/internal/adapters/outbound/telegram"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/application/usecases"
	"github.com/Diaszano/election-bot/internal/platform/config"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

func Run(parent context.Context, c config.Config) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	level := slog.LevelInfo
	_ = level.UnmarshalText([]byte(c.LogLevel))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	db, e := sqlite.Open(ctx, c.Database)
	if e != nil {
		return e
	}
	defer db.Close()
	var sender ports.NotificationSender = &RecordingSender{}
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
	settings := usecases.Settings{Location: c.Location, PollInterval: c.PollInterval, BeforeInterval: c.BeforeInterval, FinalInterval: c.FinalInterval, StaleAfter: c.StaleAfter, DelayedAfter: c.DelayedAfter, CountingStart: c.CountingStart}
	if c.Mode == "simulation" {
		settings.CountingStart = time.Time{}
	}
	svc := usecases.New(db, db, simulation.New(), sender, ports.RealClock{}, settings)
	var wg sync.WaitGroup
	start := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }
	start(func() { scheduler.Run(ctx, time.Second, "election.poll", logger, svc.PollElectionResults) })
	start(func() {
		scheduler.Run(ctx, c.NotificationInterval, "notifications.publish", logger, svc.PublishResultUpdates)
	})
	if api != nil {
		h := commands.New(svc, sender, c.Location, username)
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
	logger.Info("election-bot initialized", "source_mode", c.Mode, "telegram_enabled", c.TelegramEnabled)
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

// Package usecases orchestrates domain decisions and durable ports.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/application/services"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
	"go.opentelemetry.io/otel"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
)

type Settings struct {
	Location                                                              *time.Location
	PollInterval, BeforeInterval, FinalInterval, StaleAfter, DelayedAfter time.Duration
	CountingStart                                                         time.Time
}
type Service struct {
	state             ports.State
	deliveries        ports.Deliveries
	provider          ports.ResultsProvider
	sender            ports.NotificationSender
	clock             ports.Clock
	settings          Settings
	pollMu, publishMu sync.Mutex
}

func New(state ports.State, deliveries ports.Deliveries, provider ports.ResultsProvider, sender ports.NotificationSender, clock ports.Clock, settings Settings) *Service {
	for _, name := range []string{"election_poll", "election_poll_errors", "election_snapshot_changes", "telegram_messages_sent", "telegram_messages_edited", "telegram_notifications_failed", "telegram_api_requests"} {
		counter, e := otel.Meter("election-bot").Int64Counter(name)
		if e != nil {
			slog.Warn("metric instrument unavailable", "instrument", name)
		} else {
			counter.Add(context.Background(), 0)
		}
	}
	return &Service{state: state, deliveries: deliveries, provider: provider, sender: sender, clock: clock, settings: settings}
}
func (s *Service) GetLatestResults(ctx context.Context) (election.Snapshot, error) {
	r, e := s.state.Latest(ctx)
	return r.Snapshot, e
}
func (s *Service) GetMonitoringStatus(ctx context.Context) (ports.MonitoringStatus, error) {
	return s.state.Status(ctx)
}
func (s *Service) SubscribeUser(ctx context.Context, chat int64) (err error) {
	ctx, finish := services.Operation(ctx, "subscriptions.create")
	defer func() { finish(err) }()
	if chat <= 0 {
		return errors.New("subscriptions require private chats")
	}
	return s.state.Transaction(ctx, func(tx ports.Transaction) error {
		sub, e := tx.Subscription(chat)
		if e != nil && !errors.Is(e, ports.ErrNotFound) {
			return e
		}
		if e == nil && sub.Active {
			return nil
		}
		sub.ChatID = chat
		sub.Active = true
		sub.Generation++
		sub.ResultsEnabled = true
		sub.MilestonesEnabled = true
		latest, e := tx.Latest()
		if e != nil && !errors.Is(e, ports.ErrNoSnapshot) {
			return e
		}
		sub.Baseline = 0
		if e == nil {
			sub.Baseline = int(latest.Snapshot.Progress / 100)
		}
		if e = tx.PutSubscription(sub); e != nil {
			return e
		}
		if latest.Snapshot.ID != "" {
			status, e := tx.Status()
			if e != nil {
				return e
			}
			return tx.Enqueue(s.main(sub, latest.Snapshot, status))
		}
		return nil
	})
}
func (s *Service) UnsubscribeUser(ctx context.Context, chat int64) (err error) {
	ctx, finish := services.Operation(ctx, "subscriptions.delete")
	defer func() { finish(err) }()
	return s.state.Transaction(ctx, func(tx ports.Transaction) error {
		sub, e := tx.Subscription(chat)
		if errors.Is(e, ports.ErrNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		sub.Active = false
		if e = tx.PutSubscription(sub); e != nil {
			return e
		}
		return tx.Cancel(chat)
	})
}
func (s *Service) freshness(snapshot election.Snapshot, status ports.MonitoringStatus) services.Freshness {
	active := snapshot.Status == election.Counting
	return services.Freshness{Stale: active && (status.LastSuccess.IsZero() || s.clock.Now().Sub(status.LastSuccess) > s.settings.StaleAfter), Delayed: active && s.clock.Now().Sub(snapshot.Published) > s.settings.DelayedAfter, LastSuccess: status.LastSuccess}
}
func (s *Service) main(sub subscription.Subscription, snapshot election.Snapshot, status ports.MonitoringStatus) notification.Delivery {
	fresh := s.freshness(snapshot, status)
	return notification.Delivery{ChatID: sub.ChatID, Generation: sub.Generation, Kind: "main", Revision: fmt.Sprintf("%s:%t:%t", snapshot.Fingerprint(), fresh.Stale, fresh.Delayed), Text: services.FormatResults(snapshot, fresh, s.settings.Location), Due: s.clock.Now()}
}
func EvaluateMilestoneAlerts(previous, current election.Percentage, baseline int) []int {
	var r []int
	for _, m := range []int{25, 50, 75, 90, 95, 100} {
		if current >= election.Percentage(m*100) && previous < election.Percentage(m*100) && m > baseline {
			r = append(r, m)
		}
	}
	return r
}
func (s *Service) ProcessElectionSnapshot(ctx context.Context, r ports.FetchResult) (err error) {
	ctx, finish := services.Operation(ctx, "election.persist_snapshot")
	defer func() { finish(err) }()
	if r.Snapshot != nil {
		if e := r.Snapshot.Validate(); e != nil {
			return e
		}
	}
	acceptedChange := false
	err = s.state.Transaction(ctx, func(tx ports.Transaction) error {
		old, e := tx.Latest()
		if e != nil && !errors.Is(e, ports.ErrNoSnapshot) {
			return e
		}
		hasOld := e == nil
		status, e := tx.Status()
		if e != nil {
			return e
		}
		if r.Unchanged {
			if !hasOld || old.Validators != r.Validators {
				return errors.New("304 without matching committed representation")
			}
			status.LastSuccess = s.clock.Now()
			status.Failures = 0
			status.LastError = ""
			status.NextPoll = r.Next
			return tx.SetStatus(status)
		}
		if r.Snapshot == nil {
			return errors.New("missing result snapshot")
		}
		snapshot := *r.Snapshot
		if hasOld {
			stale, e := snapshot.Precedes(old.Snapshot)
			if e != nil {
				return e
			}
			if stale {
				return errors.New("stale upstream snapshot rejected")
			}
		}
		changed := !hasOld || !snapshot.MeaningfullyEqual(old.Snapshot)
		status.LastSuccess = s.clock.Now()
		status.Failures = 0
		status.LastError = ""
		status.NextPoll = r.Next
		fresh := s.freshness(snapshot, status)
		status.Stale = fresh.Stale
		status.Delayed = fresh.Delayed
		if e = tx.Save(snapshot, r.Validators); e != nil {
			return e
		}
		if e = tx.SetStatus(status); e != nil {
			return e
		}
		if !changed {
			return nil
		}
		acceptedChange = true
		subs, e := tx.Subscriptions()
		if e != nil {
			return e
		}
		previous := election.Percentage(0)
		if hasOld {
			previous = old.Snapshot.Progress
		}
		for _, sub := range subs {
			if sub.ResultsEnabled {
				if e = tx.Enqueue(s.main(sub, snapshot, status)); e != nil {
					return e
				}
			}
			if sub.MilestonesEnabled {
				for _, m := range EvaluateMilestoneAlerts(previous, snapshot.Progress, sub.Baseline) {
					if e = tx.Enqueue(notification.Delivery{ChatID: sub.ChatID, Generation: sub.Generation, Kind: "milestone", Milestone: m, Revision: fmt.Sprintf("milestone-%d", m), Text: services.FormatMilestone(snapshot, m, s.settings.Location), Due: s.clock.Now()}); e != nil {
						return e
					}
				}
			}
		}
		return nil
	})
	if err == nil && acceptedChange {
		services.Counter(ctx, "election_snapshot_changes")
		slog.InfoContext(ctx, "election snapshot accepted", "operation", "election.process_snapshot")
	}
	return err
}
func (s *Service) PollElectionResults(ctx context.Context) (err error) {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()
	status, e := s.state.Status(ctx)
	if e != nil {
		return e
	}
	if status.NextPoll.After(s.clock.Now()) {
		return nil
	}
	ctx, finish := services.Operation(ctx, "election.poll")
	defer func() { finish(err) }()
	services.Counter(ctx, "election_poll")
	started := time.Now()
	defer services.Duration(ctx, "election_poll_duration", started)
	latest, e := s.state.Latest(ctx)
	if e != nil && !errors.Is(e, ports.ErrNoSnapshot) {
		return e
	}
	interval := s.settings.PollInterval
	if s.clock.Now().Before(s.settings.CountingStart) {
		interval = s.settings.BeforeInterval
	}
	if latest.Snapshot.Status == election.Finalized {
		interval = s.settings.FinalInterval
	}
	r, e := s.provider.Fetch(ctx, latest.Validators)
	if e == nil {
		r.Next = maxTime(r.Next, s.clock.Now().Add(interval))
		e = s.ProcessElectionSnapshot(ctx, r)
	}
	if e != nil {
		services.Counter(ctx, "election_poll_errors")
		var api *ports.ExternalError
		wait := interval
		if errors.As(e, &api) {
			wait = max(wait, api.RetryAfter)
		}
		save := s.state.Transaction(ctx, func(tx ports.Transaction) error {
			st, x := tx.Status()
			if x != nil {
				return x
			}
			st.Failures++
			st.LastError = "upstream synchronization failed"
			st.NextPoll = s.clock.Now().Add(wait)
			return tx.SetStatus(st)
		})
		return errors.Join(e, save)
	}
	return nil
}
func maxTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}
	return a
}
func (s *Service) refreshFreshness(ctx context.Context) error {
	return s.state.Transaction(ctx, func(tx ports.Transaction) error {
		latest, e := tx.Latest()
		if errors.Is(e, ports.ErrNoSnapshot) {
			return nil
		}
		if e != nil {
			return e
		}
		st, e := tx.Status()
		if e != nil {
			return e
		}
		f := s.freshness(latest.Snapshot, st)
		if f.Stale == st.Stale && f.Delayed == st.Delayed {
			return nil
		}
		st.Stale = f.Stale
		st.Delayed = f.Delayed
		if e = tx.SetStatus(st); e != nil {
			return e
		}
		subs, e := tx.Subscriptions()
		if e != nil {
			return e
		}
		for _, sub := range subs {
			if sub.ResultsEnabled {
				if e = tx.Enqueue(s.main(sub, latest.Snapshot, st)); e != nil {
					return e
				}
			}
		}
		return nil
	})
}
func (s *Service) PublishResultUpdates(ctx context.Context) (err error) {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if e := s.refreshFreshness(ctx); e != nil {
		return e
	}
	jobs, e := s.deliveries.Pending(ctx, s.clock.Now(), 100)
	if e != nil {
		return e
	}
	if len(jobs) == 0 {
		return nil
	}
	ctx, finish := services.Operation(ctx, "notifications.publish")
	defer func() { finish(err) }()
	for _, d := range jobs {
		active := false
		if e = s.state.Transaction(ctx, func(tx ports.Transaction) error {
			sub, e := tx.Subscription(d.ChatID)
			if errors.Is(e, ports.ErrNotFound) {
				return nil
			}
			if e != nil {
				return e
			}
			active = sub.Active && sub.Generation == d.Generation
			return nil
		}); e != nil {
			return e
		}
		if !active {
			continue
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		id := d.MessageID
		if d.Kind == "main" && id != 0 {
			e = s.sender.Edit(ctx, d.ChatID, id, d.Text)
			var api *ports.ExternalError
			if errors.As(e, &api) && api.Kind == "missing_message" {
				id, e = s.sender.Send(ctx, d.ChatID, d.Text)
			}
			if errors.As(e, &api) && api.Kind == "not_modified" {
				e = nil
			}
		} else {
			id, e = s.sender.Send(ctx, d.ChatID, d.Text)
		}
		if e == nil {
			if e = s.deliveries.Complete(ctx, d, id, s.clock.Now()); e != nil {
				return e
			}
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		services.Counter(ctx, "telegram_notifications_failed")
		kind := "transient"
		var api *ports.ExternalError
		if errors.As(e, &api) {
			kind = api.Kind
		}
		if kind == "blocked" || kind == "invalid_chat" || kind == "payload" {
			if e = s.deliveries.MarkUndeliverable(ctx, d, kind); e != nil {
				return e
			}
			continue
		}
		capDelay := min(time.Second*time.Duration(1<<min(d.Attempts, 8)), 5*time.Minute)
		wait := time.Duration(rand.Int64N(int64(capDelay) + 1))
		if d.Attempts%3 == 2 {
			wait = max(wait, time.Minute)
		}
		if api != nil {
			wait = max(wait, api.RetryAfter)
		}
		if e = s.deliveries.Reschedule(ctx, d, s.clock.Now().Add(wait), kind); e != nil {
			return e
		}
	}
	return nil
}

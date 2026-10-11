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
	"strconv"
	"sync"
	"time"
)

type Settings struct {
	AllowedWhatsAppRecipients                                             []subscription.Identity
	SourceMode                                                            string
	Election                                                              election.Identity
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
	allowedMu         sync.RWMutex
	allowedWhatsApp   map[string]bool
}

func New(state ports.State, deliveries ports.Deliveries, provider ports.ResultsProvider, sender ports.NotificationSender, clock ports.Clock, settings Settings) *Service {
	if settings.SourceMode == "" {
		settings.SourceMode = "simulation"
	}
	if settings.Election.Year == 0 {
		settings.Election = election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}
	}
	for _, name := range []string{"election_poll", "election_poll_errors", "election_snapshot_changes", "telegram_messages_sent", "telegram_messages_edited", "telegram_notifications_failed", "telegram_api_requests"} {
		counter, e := otel.Meter("election-bot").Int64Counter(name)
		if e != nil {
			slog.Warn("metric instrument unavailable", "instrument", name)
		} else {
			counter.Add(context.Background(), 0)
		}
	}
	allowed := make(map[string]bool, len(settings.AllowedWhatsAppRecipients))
	for _, identity := range settings.AllowedWhatsAppRecipients {
		allowed[identity.Key()] = true
	}
	return &Service{state: state, deliveries: deliveries, provider: provider, sender: sender, clock: clock, settings: settings, allowedWhatsApp: allowed}
}
func (s *Service) GetLatestResults(ctx context.Context) (election.Snapshot, error) {
	r, e := s.state.Latest(ctx)
	return r.Snapshot, e
}
func (s *Service) GetMonitoringStatus(ctx context.Context) (ports.MonitoringStatus, error) {
	status, err := s.state.Status(ctx)
	if err != nil {
		return status, err
	}
	latest, err := s.state.Latest(ctx)
	if errors.Is(err, ports.ErrNoSnapshot) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.PublishedAt = latest.Snapshot.Published
	return status, nil
}
func (s *Service) SubscribeUser(ctx context.Context, chat int64) error {
	identity, err := subscription.NewIdentity(s.settings.Election, subscription.Telegram, fmt.Sprint(chat))
	if err != nil {
		return err
	}
	return s.Subscribe(ctx, identity)
}

func (s *Service) Subscribe(ctx context.Context, identity subscription.Identity) (err error) {
	ctx, finish := services.Operation(ctx, "subscriptions.create")
	defer func() { finish(err) }()
	if err = s.validateIdentity(identity); err != nil {
		return err
	}
	if identity.Channel == subscription.WhatsApp {
		s.allowedMu.RLock()
		defer s.allowedMu.RUnlock()
		if !s.allowedWhatsApp[identity.Key()] {
			return errors.New("WhatsApp recipient is not allowlisted")
		}
	}
	return s.state.Transaction(ctx, func(tx ports.Transaction) error { return s.subscribeTx(tx, identity, "", s.clock.Now()) })
}

func (s *Service) subscribeTx(tx ports.Transaction, identity subscription.Identity, incomingID string, at time.Time) error {
	sub, err := tx.SubscriptionByIdentity(identity)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return err
	}
	if err == nil && sub.Active {
		return nil
	}
	sub.Identity, sub.ChatID, sub.Active = identity, 0, true
	if identity.Channel == subscription.Telegram {
		sub.ChatID, _ = strconv.ParseInt(identity.Recipient, 10, 64)
	}
	sub.Generation++
	sub.ResultsEnabled, sub.MilestonesEnabled = true, true
	latest, err := tx.Latest()
	if err != nil && !errors.Is(err, ports.ErrNoSnapshot) {
		return err
	}
	sub.Baseline = 0
	if err == nil {
		sub.Baseline = int(latest.Snapshot.Progress / 100)
	}
	if err = tx.PutSubscription(sub); err != nil {
		return err
	}
	sub, err = tx.SubscriptionByIdentity(identity)
	if err != nil {
		return err
	}
	if incomingID != "" {
		if err = tx.AuditSubscription(sub, "opt_in", incomingID, at); err != nil {
			return err
		}
	}
	if latest.Snapshot.ID == "" {
		return nil
	}
	status, err := tx.Status()
	if err != nil {
		return err
	}
	return tx.Enqueue(s.main(sub, latest.Snapshot, status))
}

func (s *Service) UnsubscribeUser(ctx context.Context, chat int64) error {
	identity, err := subscription.NewIdentity(s.settings.Election, subscription.Telegram, fmt.Sprint(chat))
	if err != nil {
		return err
	}
	return s.Unsubscribe(ctx, identity)
}

func (s *Service) Unsubscribe(ctx context.Context, identity subscription.Identity) (err error) {
	ctx, finish := services.Operation(ctx, "subscriptions.delete")
	defer func() { finish(err) }()
	if err = s.validateIdentity(identity); err != nil {
		return err
	}
	return s.state.Transaction(ctx, func(tx ports.Transaction) error { return s.unsubscribeTx(tx, identity, "", s.clock.Now()) })
}

func (s *Service) unsubscribeTx(tx ports.Transaction, identity subscription.Identity, incomingID string, at time.Time) error {
	sub, err := tx.SubscriptionByIdentity(identity)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !sub.Active {
		return nil
	}
	sub.Active = false
	if incomingID != "" {
		sub.Generation++
	}
	if err = tx.PutSubscription(sub); err != nil {
		return err
	}
	if err = tx.CancelIdentity(identity); err != nil {
		return err
	}
	if incomingID != "" {
		sub, err = tx.SubscriptionByIdentity(identity)
		if err != nil {
			return err
		}
		return tx.AuditSubscription(sub, "opt_out", incomingID, at)
	}
	return nil
}

func (s *Service) validateIdentity(identity subscription.Identity) error {
	if identity.Election != s.settings.Election {
		return errors.New("subscription election does not match application")
	}
	_, err := subscription.NewIdentity(identity.Election, identity.Channel, identity.Recipient)
	return err
}

func (s *Service) ReconcileAllowedRecipients(ctx context.Context, allowed []subscription.Identity) error {
	permitted := make(map[string]bool, len(allowed))
	for _, identity := range allowed {
		if identity.Channel != subscription.WhatsApp {
			return errors.New("allowlist only accepts WhatsApp identities")
		}
		if err := s.validateIdentity(identity); err != nil {
			return err
		}
		permitted[identity.Key()] = true
	}
	s.allowedMu.Lock()
	defer s.allowedMu.Unlock()
	err := s.state.Transaction(ctx, func(tx ports.Transaction) error {
		subs, err := tx.SubscriptionsFor(s.settings.Election, subscription.WhatsApp)
		if err != nil {
			return err
		}
		for _, sub := range subs {
			if permitted[sub.Identity.Key()] {
				continue
			}
			sub.Active = false
			sub.Generation++
			if err = tx.PutSubscription(sub); err != nil {
				return err
			}
			if err = tx.CancelIdentity(sub.Identity); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.allowedWhatsApp = permitted
	return nil
}

func (s *Service) freshness(snapshot election.Snapshot, status ports.MonitoringStatus) services.Freshness {
	active := snapshot.Status == election.Counting
	return services.Freshness{Stale: active && (status.LastSuccess.IsZero() || s.clock.Now().Sub(status.LastSuccess) > s.settings.StaleAfter), Delayed: active && s.clock.Now().Sub(snapshot.Published) > s.settings.DelayedAfter, LastSuccess: status.LastSuccess}
}
func (s *Service) main(sub subscription.Subscription, snapshot election.Snapshot, status ports.MonitoringStatus) notification.Delivery {
	fresh := s.freshness(snapshot, status)
	text := services.FormatResults(snapshot, fresh, s.settings.Location)
	if sub.Identity.Channel == subscription.WhatsApp {
		text = services.FormatWhatsAppResults(snapshot, s.settings.Location, fresh)
	}
	return notification.Delivery{ChatID: sub.ChatID, Identity: sub.Identity, Generation: sub.Generation, Kind: "main", Revision: fmt.Sprintf("%s:%t:%t", snapshot.Fingerprint(), fresh.Stale, fresh.Delayed), Text: text, Due: s.clock.Now()}
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
		presentationChanged := status.Stale != fresh.Stale || status.Delayed != fresh.Delayed
		status.Stale = fresh.Stale
		status.Delayed = fresh.Delayed
		if e = tx.Save(snapshot, r.Validators); e != nil {
			return e
		}
		if e = tx.SetStatus(status); e != nil {
			return e
		}
		if !changed && !presentationChanged {
			return nil
		}
		acceptedChange = changed
		subs, e := tx.SubscriptionsFor(snapshot.Identity, "")
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
			if changed && sub.MilestonesEnabled {
				for _, m := range EvaluateMilestoneAlerts(previous, snapshot.Progress, sub.Baseline) {
					text := services.FormatMilestone(snapshot, m, s.settings.Location)
					if sub.Identity.Channel == subscription.WhatsApp {
						text = services.FormatWhatsAppMilestone(snapshot, m)
					}
					if e = tx.Enqueue(notification.Delivery{ChatID: sub.ChatID, Identity: sub.Identity, Generation: sub.Generation, Kind: "milestone", Milestone: m, Revision: fmt.Sprintf("milestone-%d", m), Text: text, Due: s.clock.Now()}); e != nil {
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

	interval := s.pollInterval(latest.Snapshot.Status)
	var r ports.FetchResult
	for attempt := 0; attempt < 3; attempt++ {
		r, e = s.provider.Fetch(ctx, latest.Validators)
		var api *ports.ExternalError
		if e == nil || attempt == 2 || !errors.As(e, &api) || api.Kind != "transient" || api.RetryAfter > 0 {
			break
		}
		retryCtx, finishRetry := services.Operation(ctx, "election.poll_retry")
		timer := time.NewTimer(time.Duration(100*(1<<attempt)+rand.IntN(100)) * time.Millisecond)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			finishRetry(retryCtx.Err())
			return retryCtx.Err()
		case <-timer.C:
			finishRetry(nil)
		}
	}
	if e == nil {
		status := latest.Snapshot.Status
		if r.Snapshot != nil {
			status = r.Snapshot.Status
		}
		interval = s.pollInterval(status)
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
func (s *Service) pollInterval(status election.Status) time.Duration {
	if status == election.Finalized {
		return s.settings.FinalInterval
	}
	if status != election.Counting && s.clock.Now().Before(s.settings.CountingStart) {
		return s.settings.BeforeInterval
	}
	return s.settings.PollInterval
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
		subs, e := tx.SubscriptionsFor(latest.Snapshot.Identity, "")
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

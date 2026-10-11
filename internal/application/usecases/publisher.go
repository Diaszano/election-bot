package usecases

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	randv2 "math/rand/v2"
	"time"

	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/application/services"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
)

const defaultPublisherBatch = 100
const defaultPublisherAttempts = 8

type PublisherOption func(*Publisher)

func WithMaxAttempts(n int) PublisherOption {
	return func(p *Publisher) {
		if n > 0 {
			p.maxAttempts = n
		}
	}
}

func WithTelemetry(enabled bool) PublisherOption {
	return func(p *Publisher) { p.telemetryEnabled = enabled }
}

type Publisher struct {
	channel          subscription.Channel
	store            ports.ChannelDeliveries
	sender           ports.ChannelSender
	clock            ports.Clock
	policy           notification.Policy
	maxAttempts      int
	telemetryEnabled bool
	gate             chan struct{}
}

func NewPublisher(channel subscription.Channel, store ports.ChannelDeliveries, sender ports.ChannelSender, clock ports.Clock, policy notification.Policy, options ...PublisherOption) *Publisher {
	p := &Publisher{channel: channel, store: store, sender: sender, clock: clock, policy: policy, maxAttempts: defaultPublisherAttempts, telemetryEnabled: true, gate: make(chan struct{}, 1)}
	for _, option := range options {
		option(p)
	}
	return p
}

// Publish processes one bounded batch. Each instance serializes its own channel;
// separate publishers keep a blocked transport from holding up another channel.
func (p *Publisher) Publish(ctx context.Context) (err error) {
	select {
	case p.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-p.gate }()
	jobs, err := p.store.PendingChannel(ctx, p.channel, p.clock.Now(), defaultPublisherBatch)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return nil
	}
	finish := func(error) {}
	if p.channel != subscription.WhatsApp || p.telemetryEnabled {
		ctx, finish = services.Operation(ctx, "notifications.publish")
	}
	defer func() { finish(err) }()
	var channelState notification.ChannelState
	stateStore, _ := p.store.(ports.ChannelStateStore)
	if stateStore != nil {
		channelState, err = stateStore.ReadChannelState(ctx, p.channel)
		if err != nil {
			return err
		}
	}
	now := p.clock.Now()
	whatsappNotConnected := p.channel == subscription.WhatsApp && channelState.PauseReason != "" && channelState.PauseReason != "connected"
	if channelState.AuthenticationRequired || whatsappNotConnected || channelState.PausedUntil.After(now) || channelState.GlobalNextSendAt.After(now) {
		due := latestFuture(now, channelState.PausedUntil, channelState.GlobalNextSendAt)
		if due.IsZero() {
			delay := time.Minute
			if whatsappNotConnected && !channelState.AuthenticationRequired {
				delay = time.Second
			}
			due = now.Add(delay)
		}
		for _, job := range jobs {
			if err = p.store.DeferDelivery(ctx, job, due); err != nil {
				return err
			}
		}
		return nil
	}
	recipientNext := map[string]time.Time{}
	for _, job := range jobs {
		if err = ctx.Err(); err != nil {
			return err
		}
		now = p.clock.Now()
		if due := recipientNext[job.Identity.Key()]; due.After(now) {
			if err = p.store.DeferDelivery(ctx, job, due); err != nil {
				return err
			}
			continue
		}
		if channelState.GlobalNextSendAt.After(now) {
			if err = p.store.DeferDelivery(ctx, job, channelState.GlobalNextSendAt); err != nil {
				return err
			}
			continue
		}
		operation, due, done := p.operation(job, now)
		if done {
			if err = p.store.ResolveAlreadyAccepted(ctx, job); err != nil {
				return err
			}
			continue
		}
		if operation == "" {
			if !due.IsZero() {
				if err = p.store.DeferDelivery(ctx, job, due); err != nil {
					return err
				}
			}
			continue
		}
		if due.After(now) {
			if err = p.store.DeferDelivery(ctx, job, due); err != nil {
				return err
			}
			continue
		}
		if err = p.deliver(ctx, job, operation); err != nil {
			return err
		}
		// Accepted sends persist the recipient cooldown inside CompleteAttempt.
		if p.policy.RecipientMin > 0 {
			recipientNext[job.Identity.Key()] = p.clock.Now().Add(p.policy.RecipientMin)
		}
		if p.policy.GlobalMin > 0 {
			channelState.GlobalNextSendAt = p.clock.Now().Add(p.policy.GlobalMin)
			if stateStore != nil {
				if err = stateStore.SaveChannelState(ctx, p.channel, channelState); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (p *Publisher) operation(job notification.Delivery, now time.Time) (notification.Operation, time.Time, bool) {
	if job.Kind != "main" {
		return notification.Send, now, false
	}
	if p.channel == subscription.Telegram {
		if job.MainState.Ref.ID != "" {
			return notification.Edit, now, false
		}
		return notification.Send, now, false
	}
	decision := notification.DecideMain(now, job.MainState, job.SemanticRevision, job.Revision, p.policy)
	return decision.Operation, decision.Due, decision.AlreadyAccepted
}

func (p *Publisher) deliver(ctx context.Context, job notification.Delivery, operation notification.Operation) error {
	attempt, err := p.prepare(ctx, job, operation)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		return err
	}
	acceptance, err := p.send(ctx, job, attempt)
	if err != nil {
		var api *ports.ExternalError
		if errors.As(err, &api) && api.Kind == "missing_message" && operation == notification.Edit {
			if p.channel == subscription.WhatsApp {
				due := job.MainState.LastSummaryAt.Add(p.policy.Digest)
				if job.MainState.LastSummaryAt.IsZero() {
					due = job.MainState.Ref.OriginalSentAt.Add(p.policy.Digest)
				}
				now := p.clock.Now()
				due = latestFuture(now, due, job.MainState.LastActivityAt.Add(p.policy.RecipientMin))
				if due.IsZero() {
					due = now
				}
				if e := p.store.DisableEditing(ctx, job, due, "missing_message"); e != nil {
					return e
				}
				if p.telemetryEnabled {
					services.Counter(ctx, "whatsapp_delivery_failures")
				}
				return nil
			}
			attempt, err = p.prepare(ctx, job, notification.Send)
			if err == nil {
				acceptance, err = p.send(ctx, job, attempt)
				operation = notification.Send
			}
		}
		if err == nil {
			return p.complete(ctx, job, attempt, acceptance)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		kind := "transient"
		if errors.As(err, &api) && api.Kind != "" {
			kind = api.Kind
		}
		if kind == "not_modified" && operation == notification.Edit {
			return p.complete(ctx, job, attempt, notification.Acceptance{Ref: job.MainState.Ref, Operation: notification.Edit, AcceptedAt: p.clock.Now(), NextOutboundAt: p.clock.Now().Add(p.policy.RecipientMin)})
		}
		if p.channel == subscription.WhatsApp && p.telemetryEnabled {
			services.Counter(ctx, "whatsapp_delivery_failures")
		}
		if kind == "rate_limited" && api != nil && api.RetryAfter > 0 {
			if stateStore, ok := p.store.(ports.ChannelStateStore); ok {
				state, readErr := stateStore.ReadChannelState(ctx, p.channel)
				if readErr != nil {
					return readErr
				}
				state.PausedUntil = p.clock.Now().Add(api.RetryAfter)
				if saveErr := stateStore.SaveChannelState(ctx, p.channel, state); saveErr != nil {
					return saveErr
				}
			}
		}
		if kind == "blocked" || kind == "invalid_chat" || kind == "payload" || job.Attempts+1 >= p.maxAttempts {
			if e := p.store.MarkDeliveryUndeliverable(ctx, job, kind); e != nil {
				return e
			}
			return nil
		}
		delay := retryDelay(job.Attempts)
		if api != nil {
			delay = max(delay, api.RetryAfter)
		}
		return p.store.RescheduleChannel(ctx, job, p.clock.Now().Add(delay), kind)
	}
	acceptance.Operation = operation
	acceptance.NextOutboundAt = p.clock.Now().Add(p.policy.RecipientMin)
	if acceptance.AcceptedAt.IsZero() {
		acceptance.AcceptedAt = p.clock.Now()
	}
	return p.complete(ctx, job, attempt, acceptance)
}

func (p *Publisher) complete(ctx context.Context, job notification.Delivery, attempt notification.Attempt, accepted notification.Acceptance) error {
	err := p.store.CompleteAttempt(ctx, job, attempt, accepted)
	if err == nil && p.channel == subscription.WhatsApp && p.telemetryEnabled {
		name := "whatsapp_messages_sent"
		if attempt.Operation == notification.Edit {
			name = "whatsapp_messages_edited"
		}
		services.Counter(ctx, name)
	}
	return err
}

func (p *Publisher) prepare(ctx context.Context, job notification.Delivery, operation notification.Operation) (notification.Attempt, error) {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		return notification.Attempt{}, fmt.Errorf("generate delivery request id: %w", err)
	}
	return p.store.PrepareAttempt(ctx, job, operation, hex.EncodeToString(b[:]))
}
func (p *Publisher) send(ctx context.Context, job notification.Delivery, attempt notification.Attempt) (notification.Acceptance, error) {
	started := time.Now()
	name := "whatsapp"
	if p.channel == subscription.Telegram {
		name = "telegram"
	}
	if !p.telemetryEnabled {
		if attempt.Operation == notification.Edit {
			return p.sender.Edit(ctx, job.Identity.Recipient, job.MainState.Ref, attempt)
		}
		return p.sender.Send(ctx, job.Identity.Recipient, attempt)
	}
	ctx, finish := services.Operation(ctx, name+map[notification.Operation]string{notification.Send: ".send_message", notification.Edit: ".edit_message"}[attempt.Operation])
	var acceptance notification.Acceptance
	var err error
	if attempt.Operation == notification.Edit {
		acceptance, err = p.sender.Edit(ctx, job.Identity.Recipient, job.MainState.Ref, attempt)
	} else {
		acceptance, err = p.sender.Send(ctx, job.Identity.Recipient, attempt)
	}
	spanErr := err
	if err != nil && p.channel == subscription.WhatsApp {
		spanErr = safeWhatsAppSpanError(err)
	}
	finish(spanErr)
	if p.channel == subscription.WhatsApp {
		services.Duration(ctx, "whatsapp_message_send_duration", started)
	} else {
		services.Duration(ctx, "notification_send_duration", started)
	}
	return acceptance, err
}

func safeWhatsAppSpanError(err error) error {
	var external *ports.ExternalError
	if errors.As(err, &external) {
		if external.Kind == "not_modified" {
			return nil
		}
		return external
	}
	return &ports.ExternalError{Kind: "transient"}
}

func retryDelay(attempt int) time.Duration {
	base := min(time.Duration(1<<min(attempt, 8))*time.Second, 5*time.Minute)
	return base/2 + time.Duration(randv2.Int64N(int64(base/2)+1))
}
func latestFuture(now time.Time, values ...time.Time) time.Time {
	var due time.Time
	for _, v := range values {
		if v.After(now) && v.After(due) {
			due = v
		}
	}
	return due
}

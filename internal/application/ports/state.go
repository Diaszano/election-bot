// Package ports defines the application's focused infrastructure contracts.
package ports

import (
	"context"
	"errors"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
	"time"
)

var ErrNoSnapshot = errors.New("no election snapshot available")
var ErrNotFound = errors.New("subscription not found")

type Validators struct{ ETag, LastModified string }
type MonitoringStatus struct {
	LastSuccess, NextPoll, PublishedAt time.Time
	Failures                           int
	LastError                          string
	Stale, Delayed                     bool
}
type StoredSnapshot struct {
	Snapshot   election.Snapshot
	Validators Validators
}
type Transaction interface {
	Latest() (StoredSnapshot, error)
	Save(election.Snapshot, Validators) error
	Subscriptions() ([]subscription.Subscription, error)
	Subscription(int64) (subscription.Subscription, error)
	SubscriptionByIdentity(subscription.Identity) (subscription.Subscription, error)
	SubscriptionsFor(election.Identity, subscription.Channel) ([]subscription.Subscription, error)
	PutSubscription(subscription.Subscription) error
	Enqueue(notification.Delivery) error
	Cancel(int64) error
	CancelIdentity(subscription.Identity) error
	RecordCommand(notification.Command) (bool, error)
	AuditSubscription(subscription.Subscription, string, string, time.Time) error
	Status() (MonitoringStatus, error)
	ChannelState(subscription.Channel) (notification.ChannelState, error)
	SetStatus(MonitoringStatus) error
}
type State interface {
	Transaction(context.Context, func(Transaction) error) error
	Latest(context.Context) (StoredSnapshot, error)
	Status(context.Context) (MonitoringStatus, error)
}
type Deliveries interface {
	Pending(context.Context, time.Time, int) ([]notification.Delivery, error)
	Complete(context.Context, notification.Delivery, int64, time.Time) error
	Reschedule(context.Context, notification.Delivery, time.Time, string) error
	MarkUndeliverable(context.Context, notification.Delivery, string) error
}

// ChannelDeliveries separates independent channel workers from the legacy Telegram publisher.
type ChannelDeliveries interface {
	PendingChannel(context.Context, subscription.Channel, time.Time, int) ([]notification.Delivery, error)
	PrepareAttempt(context.Context, notification.Delivery, notification.Operation, string) (notification.Attempt, error)
	CompleteAttempt(context.Context, notification.Delivery, notification.Attempt, notification.Acceptance) error
	RescheduleChannel(context.Context, notification.Delivery, time.Time, string) error
	DeferDelivery(context.Context, notification.Delivery, time.Time) error
	MarkDeliveryUndeliverable(context.Context, notification.Delivery, string) error
	DisableEditing(context.Context, notification.Delivery, time.Time, string) error
	ResolveAlreadyAccepted(context.Context, notification.Delivery) error
	RequeueFailed(context.Context, subscription.Channel) error
}

type ChannelStateStore interface {
	ReadChannelState(context.Context, subscription.Channel) (notification.ChannelState, error)
	SaveChannelState(context.Context, subscription.Channel, notification.ChannelState) error
}

// CommandStore executes callback work only on the transaction passed to it.
// Callbacks must not call Store or another repository method.
type CommandStore interface {
	ProcessCommand(context.Context, notification.Command, func(Transaction) error) (bool, error)
}

type ChannelSender interface {
	Send(context.Context, string, notification.Attempt) (notification.Acceptance, error)
	Edit(context.Context, string, notification.MessageRef, notification.Attempt) (notification.Acceptance, error)
}
type OffsetStore interface {
	Offset(context.Context) (int64, error)
	SetOffset(context.Context, int64) error
}
type Clock interface{ Now() time.Time }
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

type FetchResult struct {
	Snapshot   *election.Snapshot
	Validators Validators
	Unchanged  bool
	Next       time.Time
}
type ResultsProvider interface {
	Fetch(context.Context, Validators) (FetchResult, error)
}
type NotificationSender interface {
	Send(context.Context, int64, string) (int64, error)
	Edit(context.Context, int64, int64, string) error
}

// ExternalError exposes safe operation classification, never an upstream message body.
type ExternalError struct {
	Kind       string
	RetryAfter time.Duration
}

func (e *ExternalError) Error() string { return "external operation: " + e.Kind }

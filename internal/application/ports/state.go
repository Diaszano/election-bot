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
	LastSuccess, NextPoll time.Time
	Failures              int
	LastError             string
	Stale, Delayed        bool
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
	PutSubscription(subscription.Subscription) error
	Enqueue(notification.Delivery) error
	Cancel(int64) error
	Status() (MonitoringStatus, error)
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

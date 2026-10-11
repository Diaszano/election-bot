// Package notification represents durable delivery work.
package notification

import (
	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
	"time"
)

type Attempt struct {
	ID, DeliveryID, Generation int64
	Operation                  Operation
	RequestID, Revision        string
	Text, TargetMessageID      string
}

type Acceptance struct {
	Ref        MessageRef
	Operation  Operation
	AcceptedAt time.Time
}

type ChannelState struct {
	GlobalNextSendAt, PausedUntil time.Time
	AuthenticationRequired        bool
	AccountBinding, PauseReason   string
}

type Command struct {
	Identity         subscription.Identity
	IncomingID, Verb string
	ReceivedAt       time.Time
}

type Delivery struct {
	Text                string
	ID                  int64
	ChatID, Generation  int64
	Kind                string
	Revision            string
	Milestone, Attempts int
	Due                 time.Time
	Snapshot            election.Snapshot
	MessageID           int64
	SubscriberID        int64
	Identity            subscription.Identity
	EventKey            string
	SemanticRevision    string
	MainState           MainState
}

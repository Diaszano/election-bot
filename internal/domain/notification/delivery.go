// Package notification represents durable delivery work.
package notification

import (
	"github.com/Diaszano/election-bot/internal/domain/election"
	"time"
)

type Delivery struct {
	ID                  int64
	ChatID, Generation  int64
	Kind                string
	Revision            string
	Milestone, Attempts int
	Due                 time.Time
	Snapshot            election.Snapshot
	MessageID           int64
}

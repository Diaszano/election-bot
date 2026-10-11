// Package subscription represents independently managed private-chat subscriptions.
package subscription

import "time"

type Subscription struct {
	ID                                int64
	Identity                          Identity
	ChatID                            int64
	MessageID                         int64
	Active                            bool
	Generation                        int64
	Baseline                          int
	NextOutboundAt                    time.Time
	ResultsEnabled, MilestonesEnabled bool
}

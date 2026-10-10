// Package subscription represents independently managed private-chat subscriptions.
package subscription

type Subscription struct {
	ChatID                            int64
	Active                            bool
	Generation                        int64
	Baseline                          int
	ResultsEnabled, MilestonesEnabled bool
}

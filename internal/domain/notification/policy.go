// Package notification models durable delivery and channel presentation rules.
package notification

import "time"

const MaxEditAge = 14 * time.Minute

type Operation string

const (
	Send Operation = "send"
	Edit Operation = "edit"
)

type MessageRef struct {
	ID             string
	OriginalSentAt time.Time
}

type MainState struct {
	Ref                          MessageRef
	LastEditAt, LastSummaryAt    time.Time
	LastActivityAt               time.Time
	AcceptedSemanticRevision     string
	AcceptedPresentationRevision string
	EditUnavailable              bool
}

type Policy struct {
	EditMin, Digest, RecipientMin, GlobalMin time.Duration
}

type MainDecision struct {
	Operation       Operation
	Due             time.Time
	AlreadyAccepted bool
}

// DecideMain selects only work that is newer than the accepted presentation.
// An empty operation defers work; a zero due means no further automatic send is needed.
func DecideMain(now time.Time, state MainState, semanticRevision, presentationRevision string, policy Policy) MainDecision {
	if presentationRevision != "" && presentationRevision == state.AcceptedPresentationRevision {
		return MainDecision{AlreadyAccepted: true}
	}
	if state.LastActivityAt.After(now) || (!state.Ref.OriginalSentAt.IsZero() && state.Ref.OriginalSentAt.After(now)) || state.LastEditAt.After(now) || state.LastSummaryAt.After(now) {
		return MainDecision{Due: latestTime(now.Add(nonnegative(policy.RecipientMin)), state.LastActivityAt, state.LastEditAt, state.LastSummaryAt)}
	}
	if state.Ref.ID == "" || state.Ref.OriginalSentAt.IsZero() {
		return MainDecision{Operation: Send, Due: latestTime(now, state.LastActivityAt.Add(nonnegative(policy.RecipientMin)))}
	}
	if semanticRevision == state.AcceptedSemanticRevision {
		if state.EditUnavailable || now.Sub(state.Ref.OriginalSentAt) >= MaxEditAge {
			return MainDecision{}
		}
		editAt := latestTime(state.LastEditAt.Add(nonnegative(policy.EditMin)), state.LastActivityAt.Add(nonnegative(policy.RecipientMin)))
		if now.Before(editAt) {
			return MainDecision{Due: editAt}
		}
		return MainDecision{Operation: Edit, Due: now}
	}
	editAt := latestTime(state.LastEditAt.Add(nonnegative(policy.EditMin)), state.LastActivityAt.Add(nonnegative(policy.RecipientMin)))
	if !state.EditUnavailable && now.Sub(state.Ref.OriginalSentAt) < MaxEditAge {
		if now.Before(editAt) {
			return MainDecision{Due: editAt}
		}
		return MainDecision{Operation: Edit, Due: now}
	}
	digestAt := state.LastSummaryAt.Add(nonnegative(policy.Digest))
	if state.LastSummaryAt.IsZero() {
		digestAt = state.Ref.OriginalSentAt.Add(nonnegative(policy.Digest))
	}
	digestAt = latestTime(digestAt, state.LastActivityAt.Add(nonnegative(policy.RecipientMin)))
	if now.Before(digestAt) {
		return MainDecision{Due: digestAt}
	}
	return MainDecision{Operation: Send, Due: now}
}

func nonnegative(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

func latestTime(times ...time.Time) time.Time {
	var latest time.Time
	for _, t := range times {
		if t.After(latest) {
			latest = t
		}
	}
	return latest
}

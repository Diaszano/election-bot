package notification

import (
	"testing"
	"time"
)

func TestWhatsAppMainPolicy(t *testing.T) {
	now := time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC)
	policy := Policy{EditMin: time.Minute, Digest: 15 * time.Minute, RecipientMin: 3 * time.Second, GlobalMin: time.Second}
	if got := DecideMain(now, MainState{}, "s1", "p1", policy); got.Operation != Send {
		t.Fatalf("initial summary: %+v", got)
	}
	state := MainState{Ref: MessageRef{ID: "remote-1", OriginalSentAt: now.Add(-13*time.Minute - 59*time.Second)}, LastEditAt: now.Add(-time.Minute), AcceptedSemanticRevision: "s1", AcceptedPresentationRevision: "p1"}
	if got := DecideMain(now, state, "s2", "p2", policy); got.Operation != Edit {
		t.Fatalf("eligible edit: %+v", got)
	}
	state.Ref.OriginalSentAt = now.Add(-14 * time.Minute)
	state.LastSummaryAt = now.Add(-15 * time.Minute)
	if got := DecideMain(now, state, "s2", "p2", policy); got.Operation != Send {
		t.Fatalf("edit at the 14-minute boundary: %+v", got)
	}
}

func TestEditAcceptedDigestDedup(t *testing.T) {
	now := time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC)
	state := MainState{Ref: MessageRef{ID: "remote-1", OriginalSentAt: now.Add(-20 * time.Minute)}, AcceptedSemanticRevision: "same-results", AcceptedPresentationRevision: "p-fresh"}
	got := DecideMain(now, state, "same-results", "p-fresh", Policy{EditMin: time.Minute, Digest: 15 * time.Minute})
	if !got.AlreadyAccepted || got.Operation != "" {
		t.Fatalf("already accepted presentation was queued again: %+v", got)
	}
	got = DecideMain(now, state, "same-results", "p-only-clock-change", Policy{EditMin: time.Minute, Digest: 15 * time.Minute})
	if got.Operation == Send {
		t.Fatalf("freshness-only change created a digest: %+v", got)
	}
}

func TestOriginalSendAnchorAndClockRollback(t *testing.T) {
	now := time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC)
	state := MainState{Ref: MessageRef{ID: "remote-1", OriginalSentAt: now.Add(-time.Hour)}, LastActivityAt: now.Add(time.Second), AcceptedSemanticRevision: "old"}
	got := DecideMain(now, state, "new", "p-new", Policy{EditMin: time.Minute, Digest: 15 * time.Minute})
	if got.Operation != "" || !got.Due.After(now) {
		t.Fatalf("clock rollback did not defer: %+v", got)
	}
}

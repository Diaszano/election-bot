package election

import (
	"testing"
	"time"
)

func sample() Snapshot {
	return Snapshot{Identity: Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}, Source: "simulation", ID: "opaque", Published: time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC), Generated: time.Date(2026, 10, 25, 20, 0, 1, 0, time.UTC), Status: Counting, Progress: 2500, Valid: 100, Total: 110, Blank: 5, Null: 5, Candidates: []CandidateResult{{Number: "01", Name: "Fiction A", Votes: 60, Percentage: 6000}, {Number: "02", Name: "Fiction B", Votes: 40, Percentage: 4000}}}
}
func TestPercentageBounds(t *testing.T) {
	for _, s := range []string{"-1", "100.01", "NaN", ""} {
		if _, e := NewPercentage(s); e == nil {
			t.Fatal(s)
		}
	}
	p, e := NewPercentage("87.42")
	if e != nil || p.String() != "87.42" {
		t.Fatal(p, e)
	}
}
func TestSnapshotRules(t *testing.T) {
	s := sample()
	if e := s.Validate(); e != nil {
		t.Fatal(e)
	}
	if s.ReachedMilestones()[0] != 25 {
		t.Fatal("milestone")
	}
	d, p, e := s.Difference()
	if e != nil || d != 20 || p != 2000 {
		t.Fatal(d, p, e)
	}
	b := s
	b.Candidates = []CandidateResult{s.Candidates[1], s.Candidates[0]}
	if !s.MeaningfullyEqual(b) {
		t.Fatal("order changed meaning")
	}
	b.Valid = -1
	if b.Validate() == nil {
		t.Fatal("negative votes")
	}
}
func TestOpaqueOrderingAndCorrections(t *testing.T) {
	s := sample()
	b := s
	b.ID = "1"
	b.Published = s.Published.Add(time.Second)
	b.Progress = 1000
	if ok, e := b.Precedes(s); ok || e != nil {
		t.Fatal("newer correction rejected")
	}
	b.Published = s.Published
	b.Generated = s.Generated
	if _, e := b.Precedes(s); e == nil {
		t.Fatal("ambiguous generation accepted")
	}
	b = s
	b.ID = "999999"
	if old, e := b.Precedes(s); old || e != nil {
		t.Fatal("identical generation content")
	}
	b.Published = s.Published.Add(-time.Second)
	if old, _ := b.Precedes(s); !old {
		t.Fatal("stale CDN accepted")
	}
}

// Package election models validated results independently of external protocols.
package election

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Percentage stores hundredths of a percentage point.
type Percentage int64

func NewPercentage(s string) (Percentage, error) {
	parts := strings.Split(s, ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" {
		return 0, errors.New("invalid percentage")
	}
	a, e := strconv.ParseInt(parts[0], 10, 64)
	if e != nil || a < 0 || a > 100 {
		return 0, errors.New("percentage out of range")
	}
	b := int64(0)
	if len(parts) == 2 {
		if len(parts[1]) < 1 || len(parts[1]) > 2 {
			return 0, errors.New("percentage precision exceeds two decimals")
		}
		v := parts[1]
		if len(v) == 1 {
			v += "0"
		}
		b, e = strconv.ParseInt(v, 10, 64)
		if e != nil || b < 0 || b > 99 {
			return 0, errors.New("invalid fractional percentage")
		}
	}
	p := Percentage(a*100 + b)
	if p > 10000 || strings.HasPrefix(s, "-") {
		return 0, errors.New("percentage out of range")
	}
	return p, nil
}
func (p Percentage) String() string { return fmt.Sprintf("%d.%02d", p/100, p%100) }
func (p Percentage) Ratio() float64 { return float64(p) / 10000 }

type Status string

const (
	Waiting   Status = "waiting"
	Counting  Status = "counting"
	Finalized Status = "finalized"
)

type Identity struct {
	Year, Round int
	Code, Scope string
	Office      int
}
type CandidateResult struct {
	Number, Name, Party, Destination string
	Votes                            int64
	Percentage                       Percentage
}
type Snapshot struct {
	Identity                  Identity
	Source, ID                string
	Published, Generated      time.Time
	Status                    Status
	Candidates                []CandidateResult
	Valid, Blank, Null, Total int64
	Progress                  Percentage
	ConfirmedWinner           string
}

func (s Snapshot) Validate() error {
	if s.Identity.Year != 2026 || s.Identity.Code == "" || (s.Identity.Round != 1 && s.Identity.Round != 2) || s.Identity.Scope != "br" || s.Identity.Office != 1 {
		return errors.New("invalid election identity")
	}
	if s.Source != "simulation" && s.Source != "official" {
		return errors.New("invalid source")
	}
	if s.Published.IsZero() || s.Generated.IsZero() || s.ID == "" {
		return errors.New("missing publication metadata")
	}
	if s.Status != Waiting && s.Status != Counting && s.Status != Finalized {
		return errors.New("invalid election status")
	}
	if s.Progress < 0 || s.Progress > 10000 {
		return errors.New("invalid section percentage")
	}
	sum := int64(0)
	for _, v := range []int64{s.Valid, s.Blank, s.Null} {
		if v < 0 || v > math.MaxInt64-sum {
			return errors.New("invalid or overflowing vote totals")
		}
		sum += v
	}
	if s.Total < sum {
		return errors.New("inconsistent vote totals")
	}
	seen := map[string]bool{}
	winner := false
	for _, c := range s.Candidates {
		if c.Number == "" || c.Name == "" || seen[c.Number] || c.Votes < 0 || c.Percentage < 0 || c.Percentage > 10000 {
			return errors.New("invalid candidate result")
		}
		seen[c.Number] = true
		if c.Number == s.ConfirmedWinner {
			winner = true
		}
	}
	if s.ConfirmedWinner != "" && (s.Status != Finalized || !winner || s.Source != "official") {
		return errors.New("winner requires verified official finalization")
	}
	return nil
}
func (s Snapshot) Fingerprint() string {
	c := append([]CandidateResult(nil), s.Candidates...)
	sort.Slice(c, func(i, j int) bool { return c[i].Number < c[j].Number })
	s.Candidates = c
	s.ID = ""
	s.Published = time.Time{}
	s.Generated = time.Time{}
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s Snapshot) MeaningfullyEqual(b Snapshot) bool { return s.Fingerprint() == b.Fingerprint() }

// Precedes rejects stale CDN versions and ambiguous timestamp ties without ordering opaque IDs.
func (s Snapshot) Precedes(b Snapshot) (bool, error) {
	if s.Identity != b.Identity || s.Source != b.Source {
		return false, errors.New("snapshot identity changed")
	}
	if s.ID == b.ID && !s.MeaningfullyEqual(b) {
		return false, errors.New("generation contents contradicted")
	}
	if s.Published.Before(b.Published) {
		return true, nil
	}
	if s.Published.After(b.Published) {
		return false, nil
	}
	if s.Generated.Before(b.Generated) {
		return true, nil
	}
	if s.Generated.After(b.Generated) {
		return false, nil
	}
	if !s.MeaningfullyEqual(b) {
		return false, errors.New("ambiguous equal-timestamp generation")
	}
	return false, nil
}
func (s Snapshot) ReachedMilestones() []int {
	var r []int
	for _, m := range []int{25, 50, 75, 90, 95, 100} {
		if s.Progress >= Percentage(m*100) {
			r = append(r, m)
		}
	}
	return r
}
func (s Snapshot) Difference() (int64, Percentage, error) {
	if len(s.Candidates) < 2 {
		return 0, 0, nil
	}
	c := append([]CandidateResult(nil), s.Candidates...)
	sort.Slice(c, func(i, j int) bool { return c[i].Votes > c[j].Votes })
	d := c[0].Percentage - c[1].Percentage
	if d < 0 {
		d = -d
	}
	return c[0].Votes - c[1].Votes, d, nil
}

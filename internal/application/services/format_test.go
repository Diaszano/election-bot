package services

import (
	"github.com/Diaszano/election-bot/internal/domain/election"
	"strings"
	"testing"
	"time"
)

func TestBrazilianFormattingAndSimulation(t *testing.T) {
	s := election.Snapshot{Source: "simulation", Status: election.Counting, Progress: 8742, Valid: 1234567, Published: time.Date(2026, 10, 25, 22, 42, 0, 0, time.UTC), Candidates: []election.CandidateResult{{Number: "01", Name: "A <B>", Votes: 1234567, Percentage: 5235}}}
	l, _ := time.LoadLocation("America/Sao_Paulo")
	v := FormatResults(s, Freshness{Stale: true}, l)
	for _, want := range []string{"SIMULAÇÃO", "A &lt;B&gt;", "1.234.567", "52,35%", "87,42%", "19:42", "desatualizados"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %s in %s", want, v)
		}
	}
	if strings.Contains(v, "eleito") {
		t.Fatal("winner inferred")
	}
}

package services

import (
	"strings"
	"testing"
	"time"

	"github.com/Diaszano/election-bot/internal/domain/election"
)

func TestWhatsAppFormatting(t *testing.T) {
	s := election.Snapshot{Identity: election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}, Source: "simulation", ID: "fixture", Published: time.Date(2026, 10, 25, 22, 42, 0, 0, time.UTC), Generated: time.Date(2026, 10, 25, 22, 43, 0, 0, time.UTC), Status: election.Counting, Candidates: []election.CandidateResult{{Number: "01", Name: "A", Party: "P", Votes: 61245000, Percentage: 5235}, {Number: "02", Name: "B", Party: "Q", Votes: 55745000, Percentage: 4765}}, Valid: 116990000, Total: 116990000, Progress: 8742}
	text := FormatWhatsAppResults(s, time.FixedZone("BRT", -3*60*60), Freshness{Stale: true})
	for _, want := range []string{"SIMULAÇÃO — dados fictícios", "61.245.000 votos", "52,35%", "87,42%", "Fonte: simulação", "possivelmente desatualizados"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q from %s", want, text)
		}
	}
	if strings.Contains(text, "<b>") || strings.Contains(text, "eleito") {
		t.Fatalf("WhatsApp output contains markup or inferred winner: %s", text)
	}
	if got := FormatWhatsAppMilestone(s, 50); !strings.Contains(got, "SIMULAÇÃO — dados fictícios") || strings.Contains(got, "<b>") {
		t.Fatalf("bad milestone format: %s", got)
	}
}

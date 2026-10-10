// Package services provides deterministic subscriber presentation.
package services

import (
	"fmt"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"html"
	"strings"
	"time"
)

type Freshness struct {
	Stale, Delayed bool
	LastSuccess    time.Time
}

func Number(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return s
}
func percent(p election.Percentage) string { return strings.ReplaceAll(p.String(), ".", ",") + "%" }
func FormatResults(s election.Snapshot, f Freshness, l *time.Location) string {
	var b strings.Builder
	b.WriteString("🇧🇷 <b>Eleições Presidenciais 2026</b>\n📊 <b>Apuração — 2º Turno</b>\n")
	if s.Source == "simulation" {
		b.WriteString("🧪 <b>SIMULAÇÃO — dados fictícios</b>\n")
	}
	for _, c := range s.Candidates {
		fmt.Fprintf(&b, "\n<b>%s</b> (%s) — %s\n%s — %s votos\n", html.EscapeString(c.Name), html.EscapeString(c.Number), html.EscapeString(c.Party), percent(c.Percentage), Number(c.Votes))
	}
	_, gap, _ := s.Difference()
	votes, _, _ := s.Difference()
	fmt.Fprintf(&b, "\n📈 Seções totalizadas: %s\n🗳️ Votos válidos: %s\nBrancos: %s • Nulos: %s\nVotos apurados: %s\nDiferença: %s votos • %s pontos percentuais\n", percent(s.Progress), Number(s.Valid), Number(s.Blank), Number(s.Null), Number(s.Total), Number(votes), strings.TrimSuffix(percent(gap), "%"))
	if m := s.ReachedMilestones(); len(m) > 0 {
		fmt.Fprintf(&b, "Último marco: %d%%\n", m[len(m)-1])
	}
	fmt.Fprintf(&b, "🕒 Atualizado: %s\n", s.Published.In(l).Format("02/01/2006 às 15:04:05"))
	if s.Source == "official" {
		b.WriteString("📡 Fonte: TSE — Dados oficiais\n")
	} else {
		b.WriteString("📡 Fonte: simulação local\n")
	}
	switch s.Status {
	case election.Finalized:
		b.WriteString("Apuração oficialmente finalizada.\n")
	case election.Waiting:
		b.WriteString("Aguardando início da apuração.\n")
	default:
		b.WriteString("⚠️ Apuração em andamento.\n")
	}
	if s.ConfirmedWinner != "" && s.Source == "official" && s.Status == election.Finalized {
		for _, c := range s.Candidates {
			if c.Number == s.ConfirmedWinner {
				fmt.Fprintf(&b, "Resultado confirmado pelo TSE: %s eleito(a).\n", html.EscapeString(c.Name))
			}
		}
	}
	if f.Stale {
		b.WriteString("⚠️ Dados possivelmente desatualizados; sincronização indisponível.\n")
	}
	if f.Delayed {
		b.WriteString("⚠️ Publicação do resultado sem atualização recente.\n")
	}
	return b.String()
}
func FormatMilestone(s election.Snapshot, m int, l *time.Location) string {
	prefix := "📡 TSE — Dados oficiais"
	if s.Source == "simulation" {
		prefix = "🧪 SIMULAÇÃO — dados fictícios"
	}
	return fmt.Sprintf("🇧🇷 Apuração — 2º Turno\n📈 Marco de %d%% das seções totalizadas.\n%s\nAtualizado: %s\nO marco não confirma candidato eleito.", m, prefix, s.Published.In(l).Format("02/01/2006 às 15:04:05"))
}

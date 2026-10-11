package services

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Diaszano/election-bot/internal/domain/election"
)

// FormatWhatsAppResults renders an election snapshot as plain Brazilian Portuguese text.
func FormatWhatsAppResults(s election.Snapshot, l *time.Location, f Freshness) string {
	if l == nil {
		l = time.UTC
	}
	var b strings.Builder
	b.WriteString("🇧🇷 Eleições Presidenciais 2026\n📊 Apuração — 2º Turno\n")
	if s.Source == "simulation" {
		b.WriteString("🧪 SIMULAÇÃO — dados fictícios\n")
	}
	for _, c := range s.Candidates {
		share := percent(c.Percentage)
		if c.Destination != "" && c.Destination != "Válido" {
			share = "Percentual válido indisponível (" + cleanPlain(c.Destination) + ")"
		} else if c.Destination == "Válido" {
			share += " (calculado sobre votos válidos)"
		}
		fmt.Fprintf(&b, "\n%s\n%s — %s votos\n", cleanPlain(c.Name), share, Number(c.Votes))
	}
	_, gap, _ := s.Difference()
	fmt.Fprintf(&b, "\n📈 Seções totalizadas: %s\n🗳️ Votos válidos: %s\nBrancos: %s • Nulos: %s\nVotos apurados: %s\n", percent(s.Progress), Number(s.Valid), Number(s.Blank), Number(s.Null), Number(s.Total))
	if len(s.Candidates) >= 2 {
		votes, _, _ := s.Difference()
		fmt.Fprintf(&b, "📊 Diferença: %s votos — %s pontos percentuais\n", Number(votes), strings.TrimSuffix(percent(gap), "%"))
	}
	if milestones := s.ReachedMilestones(); len(milestones) > 0 {
		fmt.Fprintf(&b, "📌 Último marco: %d%%\n", milestones[len(milestones)-1])
	}
	fmt.Fprintf(&b, "\n🕒 Atualizado: %s\n", s.Published.In(l).Format("02/01/2006 às 15:04:05"))
	if s.Source == "official" {
		b.WriteString("📡 Fonte: TSE — Dados oficiais\n")
	} else {
		b.WriteString("📡 Fonte: simulação local\n")
	}
	switch s.Status {
	case election.Finalized:
		b.WriteString("Apuração oficialmente finalizada.\n")
		if s.Source == "official" && s.ConfirmedWinner != "" {
			for _, c := range s.Candidates {
				if c.Number == s.ConfirmedWinner {
					fmt.Fprintf(&b, "Resultado confirmado pelo TSE: %s eleito(a).\n", cleanPlain(c.Name))
				}
			}
		}
	case election.Waiting:
		b.WriteString("Aguardando início da apuração.\n")
	default:
		b.WriteString("⚠️ Apuração em andamento.\n")
	}
	if f.Stale {
		b.WriteString("⚠️ Dados possivelmente desatualizados; sincronização indisponível.\n")
	}
	if f.Delayed {
		b.WriteString("⚠️ Publicação do resultado sem atualização recente.\n")
	}
	return b.String()
}

// FormatWhatsAppMilestone keeps a counting alert distinct from the editable summary.
func FormatWhatsAppMilestone(s election.Snapshot, milestone int) string {
	if s.Source == "simulation" {
		return fmt.Sprintf("🇧🇷 Apuração — 2º Turno\n📈 Marco de %d%% das seções totalizadas.\n🧪 SIMULAÇÃO — dados fictícios\nAtualizado: %s\nO marco não confirma candidato eleito.", milestone, s.Published.UTC().Format("02/01/2006 às 15:04:05"))
	}
	return fmt.Sprintf("🇧🇷 Apuração — 2º Turno\n📈 Marco de %d%% das seções totalizadas.\n📡 TSE — Dados oficiais\nAtualizado: %s\nO marco não confirma candidato eleito.", milestone, s.Published.UTC().Format("02/01/2006 às 15:04:05"))
}

func cleanPlain(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

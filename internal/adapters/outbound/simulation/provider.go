// Package simulation supplies clearly fictional deterministic runoff snapshots.
package simulation

import (
	"context"
	"fmt"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"sync"
	"time"
)

type Provider struct {
	mu   sync.Mutex
	step int
}

func New() *Provider { return &Provider{} }
func (p *Provider) Fetch(ctx context.Context, v ports.Validators) (ports.FetchResult, error) {
	if e := ctx.Err(); e != nil {
		return ports.FetchResult{}, e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	progress := []int{0, 25, 50, 75, 90, 95, 100}
	i := min(p.step, len(progress)-1)
	p.step++
	s := Snapshot(progress[i])
	return ports.FetchResult{Snapshot: &s, Validators: ports.Validators{ETag: fmt.Sprintf("simulation-%d", i)}}, nil
}
func Snapshot(progress int) election.Snapshot {
	t := time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC).Add(time.Duration(progress) * time.Minute)
	total := int64(progress * 100)
	return election.Snapshot{Identity: election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}, Source: "simulation", ID: fmt.Sprintf("fictional-%d", progress), Published: t, Generated: t, Status: election.Counting, Progress: election.Percentage(progress * 100), Valid: total, Total: total, Candidates: []election.CandidateResult{{Number: "01", Name: "Candidato fictício A", Party: "Partido fictício A", Votes: total * 55 / 100, Percentage: 5500}, {Number: "02", Name: "Candidato fictício B", Party: "Partido fictício B", Votes: total * 45 / 100, Percentage: 4500}}}
}

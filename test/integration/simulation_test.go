package integration

import (
	"context"
	"github.com/Diaszano/election-bot/internal/platform/lifecycle"
	"path/filepath"
	"testing"
)

func TestDeterministicSimulationRecovery(t *testing.T) {
	if e := lifecycle.Simulate(context.Background(), filepath.Join(t.TempDir(), "simulation.db")); e != nil {
		t.Fatal(e)
	}
}

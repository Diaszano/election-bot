package tse

import (
	"context"
	"errors"
	"fmt"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, e := os.ReadFile("../../../../test/fixtures/tse/official-simulation-21270.json")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestOfficialSimulationParsing(t *testing.T) {
	l, _ := time.LoadLocation("America/Sao_Paulo")
	s, e := Parse(fixture(t), election.Identity{Year: 2026, Round: 1, Code: "21270", Scope: "br", Office: 1}, l)
	if e != nil {
		t.Fatal(e)
	}
	if s.Valid != 100982116 || s.Total != 138863131 || s.Progress != 10000 || s.ConfirmedWinner != "" || s.Source != "simulation" {
		t.Fatalf("%+v", s)
	}
	if _, e = Parse(fixture(t), election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}, l); e == nil {
		t.Fatal("first round accepted as runoff")
	}
}
func TestConditionalAndHTTPFailures(t *testing.T) {
	b := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == "v2" {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", "v2")
		w.Header().Set("Cache-Control", "max-age=46")
		w.Write(b)
	}))
	defer srv.Close()
	l, _ := time.LoadLocation("America/Sao_Paulo")
	p := New(srv.Client(), srv.URL, election.Identity{Year: 2026, Round: 1, Code: "21270", Scope: "br", Office: 1}, l)
	r, e := p.Fetch(context.Background(), ports.Validators{})
	if e != nil || r.Validators.ETag != "v2" || r.Snapshot == nil {
		t.Fatal(r, e)
	}
	r, e = p.Fetch(context.Background(), r.Validators)
	if e != nil || !r.Unchanged {
		t.Fatal(r, e)
	}
}

func TestHTTPFailurePoliciesAndMalformedValidators(t *testing.T) {
	for _, x := range []struct {
		status     int
		body, kind string
		minimum    time.Duration
	}{{404, "", "not_published", 15 * time.Minute}, {429, "", "rate_limited", 10 * time.Minute}, {500, "", "transient", 0}, {200, `{"bad":"json"}`, "", 0}, {304, "", "", 0}} {
		t.Run(fmt.Sprint(x.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", "unaccepted")
				w.WriteHeader(x.status)
				fmt.Fprint(w, x.body)
			}))
			defer server.Close()
			p := New(server.Client(), server.URL, election.Identity{Year: 2026, Round: 1, Code: "21270", Scope: "br", Office: 1}, time.UTC)
			r, e := p.Fetch(context.Background(), ports.Validators{})
			if e == nil {
				t.Fatal("bad response accepted")
			}
			if r.Validators.ETag != "" {
				t.Fatal("unaccepted validators leaked")
			}
			if x.kind != "" {
				var api *ports.ExternalError
				if !errors.As(e, &api) || api.Kind != x.kind || api.RetryAfter < x.minimum {
					t.Fatal(e)
				}
			}
		})
	}
}

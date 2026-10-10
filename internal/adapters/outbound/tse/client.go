// Package tse decodes verified simulation artifacts; production mappings remain gated.
package tse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/application/services"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Provider struct {
	client   *http.Client
	url      string
	identity election.Identity
	location *time.Location
}

func New(c *http.Client, url string, id election.Identity, l *time.Location) *Provider {
	copy := *c
	transport := c.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	copy.Transport = otelhttp.NewTransport(transport, otelhttp.WithSpanNameFormatter(func(string, *http.Request) string { return "tse.http" }))
	return &Provider{&copy, url, id, l}
}
func (p *Provider) Fetch(ctx context.Context, v ports.Validators) (result ports.FetchResult, err error) {
	ctx, finish := services.Operation(ctx, "tse.fetch_results")
	defer func() { finish(err) }()
	var out ports.FetchResult
	req, e := http.NewRequestWithContext(ctx, "GET", p.url, nil)
	if e != nil {
		return out, &ports.ExternalError{Kind: "invalid_request"}
	}
	if v.ETag != "" {
		req.Header.Set("If-None-Match", v.ETag)
	}
	if v.LastModified != "" {
		req.Header.Set("If-Modified-Since", v.LastModified)
	}
	r, e := p.client.Do(req)
	if e != nil {
		return out, &ports.ExternalError{Kind: "transient"}
	}
	defer r.Body.Close()
	out.Next = time.Now().Add(cacheWait(r.Header))
	if r.StatusCode == 304 {
		if v.ETag == "" && v.LastModified == "" {
			return out, errors.New("304 without committed representation validators")
		}
		out.Unchanged = true
		out.Validators = v
		return out, nil
	}
	if r.StatusCode != 200 {
		kind := "transient"
		wait := retryAfter(r.Header.Get("Retry-After"))
		switch r.StatusCode {
		case 404:
			kind = "not_published"
			wait = max(wait, 15*time.Minute)
		case 429, 403:
			kind = "rate_limited"
			wait = max(wait, 10*time.Minute)
		default:
			if r.StatusCode < 500 {
				kind = "permanent"
			}
		}
		return ports.FetchResult{}, &ports.ExternalError{Kind: kind, RetryAfter: wait}
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024+1))
	if e != nil || len(b) > 4*1024*1024 {
		return ports.FetchResult{}, errors.New("invalid result body")
	}
	s, e := Parse(b, p.identity, p.location)
	if e != nil {
		return ports.FetchResult{}, e
	}
	out.Snapshot = &s
	out.Validators = ports.Validators{ETag: r.Header.Get("ETag"), LastModified: r.Header.Get("Last-Modified")}
	return out, nil
}
func retryAfter(s string) time.Duration {
	if n, e := strconv.Atoi(s); e == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, e := http.ParseTime(s); e == nil {
		return max(time.Until(t), 0)
	}
	return 0
}
func cacheWait(h http.Header) time.Duration {
	var wait time.Duration
	for _, part := range strings.Split(h.Get("Cache-Control"), ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && k == "max-age" {
			n, _ := strconv.Atoi(v)
			age, _ := strconv.Atoi(h.Get("Age"))
			wait = time.Duration(max(n-age, 0)) * time.Second
		}
	}
	return max(wait, retryAfter(h.Get("Retry-After")))
}
func Parse(b []byte, id election.Identity, l *time.Location) (s election.Snapshot, err error) {
	var d struct {
		Ele, T, F, TPAbr, CDabr, Dg, Hg, Idg, Dt, Ht, And string
		S                                                 struct{ Pst string }
		V                                                 struct{ Tv, Vv, Vb, Tvn string }
		Carg                                              []struct {
			Cd  string
			Agr []struct {
				Par []struct {
					Sg   string
					Cand []struct{ N, Nmu, Nm, Vap, Pvap, Dvt, St string }
				}
			}
		}
	}
	if e := json.Unmarshal(b, &d); e != nil {
		return s, errors.New("malformed TSE JSON")
	}
	round, e := strconv.Atoi(d.T)
	if e != nil || d.Ele != id.Code || round != id.Round || d.TPAbr != "br" || d.CDabr != "br" || d.F != "s" {
		return s, errors.New("TSE identity or phase mismatch; official schema not verified")
	}
	s.Identity = id
	s.Source = "simulation"
	s.ID = d.Idg
	s.Status = election.Counting
	parseTime := func(date, hour string) (time.Time, error) {
		return time.ParseInLocation("02/01/2006 15:04:05", date+" "+hour, l)
	}
	if s.Published, err = parseTime(d.Dt, d.Ht); err != nil {
		return s, errors.New("invalid TSE publication time")
	}
	if s.Generated, err = parseTime(d.Dg, d.Hg); err != nil {
		return s, errors.New("invalid TSE generation time")
	}
	num := func(raw string) (int64, error) {
		n, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || n < 0 {
			return 0, errors.New("invalid TSE vote count")
		}
		return n, nil
	}
	for _, f := range []struct {
		raw string
		p   *int64
	}{{d.V.Tv, &s.Total}, {d.V.Vv, &s.Valid}, {d.V.Vb, &s.Blank}, {d.V.Tvn, &s.Null}} {
		if *f.p, err = num(f.raw); err != nil {
			return s, err
		}
	}
	if s.Progress, err = election.NewPercentage(strings.ReplaceAll(d.S.Pst, ",", ".")); err != nil {
		return s, err
	}
	found := false
	for _, office := range d.Carg {
		if office.Cd != "1" {
			continue
		}
		found = true
		for _, a := range office.Agr {
			for _, p := range a.Par {
				for _, c := range p.Cand {
					votes, e := num(c.Vap)
					if e != nil {
						return s, e
					}
					name := c.Nmu
					if name == "" {
						name = c.Nm
					}
					pct := election.Percentage(0)
					if c.Dvt == "Válido" && s.Valid > 0 {
						pct = election.Percentage(math.Round(float64(votes) / float64(s.Valid) * 10000))
					}
					s.Candidates = append(s.Candidates, election.CandidateResult{Number: c.N, Name: name, Party: p.Sg, Destination: c.Dvt, Votes: votes, Percentage: pct})
				}
			}
		}
	}
	if !found {
		return s, fmt.Errorf("missing presidential office")
	}
	return s, s.Validate()
}

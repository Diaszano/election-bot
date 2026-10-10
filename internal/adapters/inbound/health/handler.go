// Package health exposes local process and persistence health.
package health

import (
	"context"
	"net/http"
)

func New(ready func(context.Context) error) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"live"}`))
	})
	m.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if ready(r.Context()) != nil {
			w.WriteHeader(503)
			w.Write([]byte(`{"status":"not_ready"}`))
			return
		}
		w.Write([]byte(`{"status":"ready"}`))
	})
	return m
}

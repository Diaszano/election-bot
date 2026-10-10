package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheckUsesConfiguredPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health/live" {
			t.Fatal(r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	t.Setenv("HTTP_LISTEN_ADDRESS", strings.TrimPrefix(server.URL, "http://"))
	if e := healthcheck(); e != nil {
		t.Fatal(e)
	}
}

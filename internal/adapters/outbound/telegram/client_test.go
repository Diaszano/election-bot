package telegram

import (
	"context"
	"errors"
	"fmt"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramClassificationAndRedaction(t *testing.T) {
	for _, x := range []struct {
		code       int
		body, kind string
	}{{429, `{"ok":false,"error_code":429,"description":"sentinel-secret","parameters":{"retry_after":2}}`, "rate_limited"}, {403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`, "blocked"}, {400, `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`, "not_modified"}, {400, `{"ok":false,"error_code":400,"description":"Bad Request: message to edit not found"}`, "missing_message"}} {
		t.Run(x.kind, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(x.code); fmt.Fprint(w, x.body) }))
			defer s.Close()
			c := New(s.Client(), s.URL, "sentinel-secret", 0)
			_, e := c.Send(context.Background(), 123, "private")
			var api *ports.ExternalError
			if !errors.As(e, &api) || api.Kind != x.kind || strings.Contains(e.Error(), "sentinel-secret") {
				t.Fatal(e)
			}
		})
	}
}
func TestSendAndEdit(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ok":true,"result":{"message_id":42}}`) }))
	defer s.Close()
	c := New(s.Client(), s.URL, "token", 0)
	id, e := c.Send(context.Background(), 1, "text")
	if e != nil || id != 42 {
		t.Fatal(id, e)
	}
	if e = c.Edit(context.Background(), 1, 42, "new"); e != nil {
		t.Fatal(e)
	}
}

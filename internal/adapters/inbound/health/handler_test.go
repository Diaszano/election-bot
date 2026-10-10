package health

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
)

func TestLocalReadinessAndLiveness(t *testing.T) {
	for _, x := range []struct {
		path string
		err  error
		want int
	}{{"/health/live", errors.New("upstream unavailable"), 200}, {"/health/ready", nil, 200}, {"/health/ready", errors.New("storage unavailable"), 503}} {
		h := New(func(context.Context) error { return x.err })
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", x.path, nil))
		if w.Code != x.want {
			t.Fatal(w.Code, x)
		}
	}
}

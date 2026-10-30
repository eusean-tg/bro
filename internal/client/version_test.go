package client_test

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/eusean-tg/bro/internal/server"
	"github.com/eusean-tg/bro/internal/store"
)

type recorder struct {
	mu     sync.Mutex
	bodies []server.LogRequest
}

func (r *recorder) add(b server.LogRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, b)
}

func (r *recorder) all() []server.LogRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]server.LogRequest(nil), r.bodies...)
}

// retiring answers the first n requests with 412, as a daemon from an
// older build would, and the rest with an empty log.
func retiring(n int, rec *recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body server.LogRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.add(body)

		w.Header().Set("Content-Type", "application/json")
		if len(rec.all()) <= n {
			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte(`{"error":"version mismatch"}`))
			return
		}
		_, _ = w.Write([]byte(`{"messages":[]}`))
	}
}

func TestA412IsRetriedWithTheFullRequest(t *testing.T) {
	var rec recorder
	c := clientFor(t, retiring(1, &rec))

	if _, err := c.Log(t.Context(), "alpha", store.LogOptions{}, 0); err != nil {
		t.Fatalf("Log after one 412: %v", err)
	}

	bodies := rec.all()
	if len(bodies) != 2 {
		t.Fatalf("server saw %d requests, want 2: the 412 and its retry", len(bodies))
	}
	if bodies[1].Workspace != "alpha" {
		t.Errorf("retry body = %+v, want workspace alpha: a retry must resend the whole body", bodies[1])
	}
}

func TestRetriesAfter412AreBounded(t *testing.T) {
	var rec recorder
	c := clientFor(t, retiring(1000, &rec))

	start := time.Now()
	if _, err := c.Log(t.Context(), "alpha", store.LogOptions{}, 0); err == nil {
		t.Fatal("Log succeeded against a server that always answers 412")
	}
	if n := len(rec.all()); n < 2 || n > 10 {
		t.Errorf("server saw %d requests, want a small bounded number of retries", n)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("gave up after %v", elapsed)
	}
}

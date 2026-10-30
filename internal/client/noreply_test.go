package client_test

import (
	"net/http"
	"sync"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

// hangUp closes the connection without a reply, as a retiring daemon's
// listener does to a connection still queued on it when it closes.
func hangUp(t *testing.T, w http.ResponseWriter) {
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		t.Errorf("Hijack: %v", err)
		return
	}
	_ = conn.Close()
}

// counted serves each request with the handler for its 1-based position,
// the last one serving every request after it.
type counted struct {
	mu    sync.Mutex
	calls int
}

func (c *counted) serve(steps ...func(http.ResponseWriter)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.calls++
		n := c.calls
		c.mu.Unlock()

		steps[min(n, len(steps))-1](w)
	}
}

func (c *counted) n() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func status(code int) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Connection", "close")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"error":"going away"}`))
	}
}

func emptyLog(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"messages":[]}`))
}

func TestARequestDroppedByADaemonGoingAwayIsRetried(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
	}{
		{"after a 412", http.StatusPreconditionFailed}, // an older build retiring
		{"after a 503", http.StatusServiceUnavailable}, // a daemon stopping mid-wait
	} {
		t.Run(tc.name, func(t *testing.T) {
			var srv counted
			c := clientFor(t, srv.serve(
				status(tc.code),
				func(w http.ResponseWriter) { hangUp(t, w) }, // the retry, queued as the listener closes
				emptyLog, // the next daemon
			))

			if _, err := c.Log(t.Context(), "alpha", store.LogOptions{}, 0); err != nil {
				t.Fatalf("Log = %v, want the retry after the dropped request to reach the next daemon", err)
			}
			if n := srv.n(); n != 3 {
				t.Errorf("server saw %d requests, want 3: the refusal, the dropped retry, and the one that succeeds", n)
			}
		})
	}
}

func TestARequestDroppedWithoutWarningIsNotRetried(t *testing.T) {
	var srv counted
	c := clientFor(t, srv.serve(
		func(w http.ResponseWriter) { hangUp(t, w) },
		emptyLog,
	))

	// With no 412 or 503 first, nothing says the daemon left the request
	// alone: it may have stored the tell before the connection dropped,
	// and a retry would post it twice.
	if _, err := c.Tell(t.Context(), "alpha", "writer", "once", nil, store.KindNote); err == nil {
		t.Fatal("Tell = nil after the daemon hung up, want the error")
	}
	if n := srv.n(); n != 1 {
		t.Errorf("server saw %d requests, want 1: a dropped request is retried only after the daemon said it was going away", n)
	}
}

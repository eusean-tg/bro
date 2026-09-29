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

func TestPullSendsTheWaitInMilliseconds(t *testing.T) {
	var mu sync.Mutex
	var got []server.PullRequest
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		var body server.PullRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = append(got, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[],"latest_id":0,"unread_count":0,"timed_out":false}`))
	})

	for _, wait := range []time.Duration{0, 1500 * time.Millisecond, 10 * time.Minute} {
		if _, err := c.Pull(t.Context(), "alpha", "reader", store.PullOptions{}, wait); err != nil {
			t.Fatalf("Pull(wait %v): %v", wait, err)
		}
	}

	want := []int64{0, 1500, 600000}
	if len(got) != len(want) {
		t.Fatalf("server saw %d requests, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].WaitMS != w {
			t.Errorf("request %d: wait_ms = %d, want %d", i, got[i].WaitMS, w)
		}
	}
}

func TestPullReportsATimedOutWait(t *testing.T) {
	st, c := newClient(t)
	register(t, st, "alpha", "writer")
	register(t, st, "alpha", "reader")
	if _, err := c.Tell(t.Context(), "alpha", "writer", "to the room", nil); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	res, err := c.Pull(t.Context(), "alpha", "reader", store.PullOptions{}, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if !res.TimedOut {
		t.Error("TimedOut = false for a wait that nothing woke")
	}
	if len(res.Messages) != 1 || res.Messages[0].Body != "to the room" {
		t.Errorf("messages = %+v, want the ambient message: a timed-out wait still returns what arrived", res.Messages)
	}
}

func TestPullReturnsOnceAMentionArrives(t *testing.T) {
	st, c := newClient(t)
	register(t, st, "alpha", "writer")
	register(t, st, "alpha", "reader")

	type result struct {
		res server.PullResponse
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := c.Pull(t.Context(), "alpha", "reader", store.PullOptions{}, time.Minute)
		done <- result{res, err}
	}()

	// Whether the tell lands before or after the pull starts waiting, the
	// pull must see it: before, its first check finds it; after, the
	// publish wakes it.
	if _, err := c.Tell(t.Context(), "alpha", "writer", "for you", []string{"reader"}); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Pull: %v", r.err)
		}
		if r.res.TimedOut {
			t.Error("TimedOut = true for a pull a mention woke")
		}
		if len(r.res.Messages) != 1 || r.res.Messages[0].Body != "for you" {
			t.Errorf("messages = %+v, want [for you]", r.res.Messages)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Pull still waiting 5s after a mention; it should return at once")
	}
}

// unavailable answers the first n requests with 503, as a daemon stopping
// mid-wait does, and the rest with an empty pull.
func unavailable(n int, rec *recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body server.PullRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.add(server.LogRequest{Workspace: body.Workspace})

		w.Header().Set("Content-Type", "application/json")
		if len(rec.all()) <= n {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"shutting down"}`))
			return
		}
		_, _ = w.Write([]byte(`{"messages":[],"latest_id":0,"unread_count":0,"timed_out":false}`))
	}
}

func TestA503IsRetried(t *testing.T) {
	var rec recorder
	c := clientFor(t, unavailable(1, &rec))

	if _, err := c.Pull(t.Context(), "alpha", "reader", store.PullOptions{}, time.Minute); err != nil {
		t.Fatalf("Pull after one 503: %v", err)
	}
	if n := len(rec.all()); n != 2 {
		t.Errorf("server saw %d requests, want 2: the 503 and its retry", n)
	}
}

func TestRetriesAfter503AreBounded(t *testing.T) {
	var rec recorder
	c := clientFor(t, unavailable(1000, &rec))

	if _, err := c.Pull(t.Context(), "alpha", "reader", store.PullOptions{}, time.Minute); err == nil {
		t.Fatal("Pull succeeded against a server that always answers 503")
	}
	if n := len(rec.all()); n < 2 || n > 10 {
		t.Errorf("server saw %d requests, want a small bounded number of retries", n)
	}
}

func TestOtherFailuresAreNotRetried(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"disk full"}`))
	})

	// A 500 may come after the write committed; resending a tell could post
	// it twice. Only 412 and 503 promise the daemon changed nothing.
	if _, err := c.Tell(t.Context(), "alpha", "writer", "once", nil); err == nil {
		t.Fatal("Tell succeeded against a server that answers 500")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("server saw %d requests, want 1: a 500 must not be retried", calls)
	}
}

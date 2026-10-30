package server_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/eusean-tg/bro/internal/server"
)

// A log with wait_ms is a long poll: it answers as soon as the workspace has
// a message after the given id, and with nothing once wait_ms passes. Unlike
// a pull's wait, any message counts, not only one that mentions somebody.
// These tests run in a synctest bubble; wait_test.go explains how.

func logWaitBody(after int64, waitMS string, extra string) string {
	return `{"workspace":"alpha","after":` + jsonInt(after) + `,"wait_ms":` + waitMS + extra + `}`
}

func TestALogWaitAnswersAtOnceWhenAMessageIsAlreadyAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)
		seen := say(t, h, "writer", "seen")
		say(t, h, "writer", "not yet seen")

		start := time.Now()
		rec := post(t, h, "/log", logWaitBody(seen, tenMinutes, ""))
		wantStatus(t, rec, http.StatusOK)

		if d := time.Since(start); d != 0 {
			t.Errorf("log waited %v with a message already after %d, want no wait at all", d, seen)
		}
		if b := bodies(decode[server.LogResponse](t, rec).Messages); !slices.Equal(b, []string{"not yet seen"}) {
			t.Errorf("messages = %q, want [not yet seen]", b)
		}
	})
}

func TestALogWaitBlocksUntilAnyMessageIsPosted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)
		seen := say(t, h, "writer", "seen")

		done := sending(t.Context(), h, "/log", logWaitBody(seen, tenMinutes, ""))
		if rec := answered(done); rec != nil {
			t.Fatalf("log returned with nothing after %d: %d %s", seen, rec.Code, rec.Body.String())
		}

		time.Sleep(time.Minute)
		say(t, h, "writer", "to the room")

		rec := answered(done)
		if rec == nil {
			t.Fatal("log still waiting after a message was posted; a log wakes for every message, mentioned or not")
		}
		wantStatus(t, rec, http.StatusOK)
		if b := bodies(decode[server.LogResponse](t, rec).Messages); !slices.Equal(b, []string{"to the room"}) {
			t.Errorf("messages = %q, want [to the room]", b)
		}
	})
}

// Ids come from one sequence shared by every workspace, so a message posted
// elsewhere can have an id after the one a log is waiting past.
func TestALogWaitIgnoresOtherWorkspaces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)
		seen := say(t, h, "writer", "seen")
		register(t, st, "beta", "writer")
		wantStatus(t, post(t, h, "/tell", `{"workspace":"beta","sender":"writer","body":"in beta"}`), http.StatusOK)

		done := sending(t.Context(), h, "/log", logWaitBody(seen, tenMinutes, ""))
		if rec := answered(done); rec != nil {
			t.Fatalf("alpha's log returned for a message in beta: %s", rec.Body.String())
		}
	})
}

// The wait only has to learn that something is after the id; the answer
// then carries everything after it, shaped by last like any log.
func TestALogWaitAnswersWithEveryMessageAfterTheID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)
		for _, b := range []string{"one", "two", "three"} {
			say(t, h, "writer", b)
		}

		all := bodies(decode[server.LogResponse](t, post(t, h, "/log", logWaitBody(0, tenMinutes, ""))).Messages)
		if want := []string{"one", "two", "three"}; !slices.Equal(all, want) {
			t.Errorf("messages = %q, want %q", all, want)
		}

		last := bodies(decode[server.LogResponse](t, post(t, h, "/log", logWaitBody(0, tenMinutes, `,"last":2`))).Messages)
		if want := []string{"two", "three"}; !slices.Equal(last, want) {
			t.Errorf("messages with last 2 = %q, want %q", last, want)
		}
	})
}

// Running out of time is how a long poll ends when the room is quiet, not a
// failure: the client asks again.
func TestALogWaitThatTimesOutIsAnEmpty200(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)
		seen := say(t, h, "writer", "seen")

		start := time.Now()
		rec := post(t, h, "/log", logWaitBody(seen, `1500`, ""))
		wantStatus(t, rec, http.StatusOK)

		if d := time.Since(start); d != 1500*time.Millisecond {
			t.Errorf("log returned after %v, want exactly the 1.5s wait_ms", d)
		}
		if b := bodies(decode[server.LogResponse](t, rec).Messages); len(b) != 0 {
			t.Errorf("messages = %q, want none", b)
		}
	})
}

// A daemon stopping cancels every request's context. The 503 is what lets
// the client retry onto the next daemon and keep following.
func TestACancelledLogWaitIs503(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)

		ctx, cancel := context.WithCancel(t.Context())
		done := sending(ctx, h, "/log", logWaitBody(0, tenMinutes, ""))
		answered(done)
		cancel()

		rec := answered(done)
		if rec == nil {
			t.Fatal("log still waiting after its request was cancelled")
		}
		wantError(t, rec, http.StatusServiceUnavailable)
	})
}

func TestALogWithoutWaitMSNeverWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)
		seen := say(t, h, "writer", "seen")

		start := time.Now()
		rec := post(t, h, "/log", `{"workspace":"alpha","after":`+jsonInt(seen)+`}`)
		wantStatus(t, rec, http.StatusOK)

		if d := time.Since(start); d != 0 {
			t.Errorf("a log without wait_ms took %v", d)
		}
		if b := bodies(decode[server.LogResponse](t, rec).Messages); len(b) != 0 {
			t.Errorf("messages = %q, want none after the latest", b)
		}
	})
}

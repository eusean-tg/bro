package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/eusean-tg/bro/internal/server"
	"github.com/eusean-tg/bro/internal/store"
)

// Every test here runs inside synctest.Test. Time in the bubble is fake: it
// stands still while any goroutine can run, and jumps forward the moment
// every goroutine is blocked. A ten-minute wait that nothing interrupts
// therefore returns instantly, and synctest.Wait() returns once every
// goroutine has either finished or blocked, so a test can ask "is the pull
// still waiting?" without sleeping and guessing.
//
// The store and the handler must be created inside the bubble, so each test
// calls newServer itself.

const tenMinutes = `600000` // wait_ms

// room registers reader, other and writer in alpha.
func room(t *testing.T, st *store.Store) {
	t.Helper()
	for _, name := range []string{"reader", "other", "writer"} {
		register(t, st, "alpha", name)
	}
}

// say posts a message through the handler, as a client's tell would, so the
// tell handler's publish runs. It returns the message's id.
func say(t *testing.T, h http.Handler, sender, body string, to ...string) int64 {
	t.Helper()

	b, err := json.Marshal(server.TellRequest{Workspace: "alpha", Sender: sender, Body: body, To: to})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := post(t, h, "/tell", string(b))
	wantStatus(t, rec, http.StatusOK)
	return decode[server.TellResponse](t, rec).ID
}

// sending starts a request to path in its own goroutine, under ctx, and
// returns the channel its response arrives on.
func sending(ctx context.Context, h http.Handler, path, body string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Bro-Version", testVersion)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		done <- rec
	}()
	return done
}

// answered waits for every goroutine in the bubble to settle, then reports
// the response if the request has returned, or nil if it is still waiting.
func answered(done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	synctest.Wait()
	select {
	case rec := <-done:
		return rec
	default:
		return nil
	}
}

func waitBody(me, waitMS string, extra string) string {
	return `{"workspace":"alpha","me":"` + me + `","wait_ms":` + waitMS + extra + `}`
}

func bodies(msgs []store.Message) []string {
	out := []string{}
	for _, m := range msgs {
		out = append(out, m.Body)
	}
	return out
}

func TestWaitReturnsAtOnceWhenAMentionIsAlreadyUnread(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)
		say(t, h, "writer", "for you", "reader")

		start := time.Now()
		rec := post(t, h, "/pull", waitBody("reader", tenMinutes, ""))
		wantStatus(t, rec, http.StatusOK)

		if d := time.Since(start); d != 0 {
			t.Errorf("pull waited %v with a mention already unread, want no wait at all", d)
		}
		got := decode[server.PullResponse](t, rec)
		if got.TimedOut {
			t.Error("timed_out = true for a pull that was woken")
		}
		if b := bodies(got.Messages); len(b) != 1 || b[0] != "for you" {
			t.Errorf("messages = %q, want [for you]", b)
		}
	})
}

func TestWaitBlocksUntilAMentionArrives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)

		done := sending(t.Context(), h, "/pull", waitBody("reader", tenMinutes, ""))
		if rec := answered(done); rec != nil {
			t.Fatalf("pull returned before anything was said: %d %s", rec.Code, rec.Body.String())
		}

		time.Sleep(time.Minute)
		say(t, h, "writer", "for you", "reader")

		rec := answered(done)
		if rec == nil {
			t.Fatal("pull still waiting after a message mentioned the reader; does tell publish?")
		}
		wantStatus(t, rec, http.StatusOK)
		got := decode[server.PullResponse](t, rec)
		if got.TimedOut {
			t.Error("timed_out = true for a pull woken a minute into a ten-minute wait")
		}
		if b := bodies(got.Messages); len(b) != 1 || b[0] != "for you" {
			t.Errorf("messages = %q, want [for you]", b)
		}
	})
}

func TestWaitSleepsThroughAmbientMessages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)

		done := sending(t.Context(), h, "/pull", waitBody("reader", tenMinutes, ""))
		answered(done)
		say(t, h, "writer", "to the room")
		say(t, h, "writer", "for someone else", "other")
		say(t, h, "reader", "note to self", "reader")

		if rec := answered(done); rec != nil {
			t.Fatalf("pull returned for messages that do not mention the reader: %s", rec.Body.String())
		}
	})
}

func TestAWakeForOneReaderDoesNotEndAnothersWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)

		// Both waiters share the workspace, so the hub wakes both. Only the
		// reader's re-check finds a mention; other's finds nothing and must
		// go back to waiting.
		reader := sending(t.Context(), h, "/pull", waitBody("reader", tenMinutes, ""))
		other := sending(t.Context(), h, "/pull", waitBody("other", tenMinutes, ""))
		answered(reader)
		say(t, h, "writer", "for you", "reader")

		if rec := answered(reader); rec == nil {
			t.Error("reader still waiting after being mentioned")
		}
		if rec := answered(other); rec != nil {
			t.Errorf("other's wait ended on a wake that was not for it: %s", rec.Body.String())
		}

		// And other's wait still works afterwards.
		say(t, h, "writer", "now you", "other")
		if rec := answered(other); rec == nil {
			t.Error("other not woken by its own mention after a wake meant for reader")
		}
	})
}

func TestAWaitThatTimesOutPullsAnyway(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)
		say(t, h, "writer", "to the room")

		start := time.Now()
		rec := post(t, h, "/pull", waitBody("reader", `1500`, ""))
		wantStatus(t, rec, http.StatusOK)

		if d := time.Since(start); d != 1500*time.Millisecond {
			t.Errorf("pull returned after %v, want exactly the 1.5s wait_ms", d)
		}
		got := decode[server.PullResponse](t, rec)
		if !got.TimedOut {
			t.Error("timed_out = false for a wait nothing woke")
		}
		if b := bodies(got.Messages); len(b) != 1 || b[0] != "to the room" {
			t.Errorf("messages = %q, want [to the room]: a timed-out wait still returns what arrived", b)
		}

		again := decode[server.PullResponse](t, post(t, h, "/pull", `{"workspace":"alpha","me":"reader"}`))
		if len(again.Messages) != 0 {
			t.Errorf("pull after the timeout returned %q; the timed-out pull should have advanced the cursor", bodies(again.Messages))
		}
	})
}

func TestAWaitWithPeekLeavesTheCursor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)

		done := sending(t.Context(), h, "/pull", waitBody("reader", tenMinutes, `,"peek":true`))
		answered(done)
		say(t, h, "writer", "for you", "reader")
		rec := answered(done)
		if rec == nil {
			t.Fatal("peeking pull still waiting after a mention")
		}
		wantStatus(t, rec, http.StatusOK)

		plain := decode[server.PullResponse](t, post(t, h, "/pull", `{"workspace":"alpha","me":"reader"}`))
		if b := bodies(plain.Messages); len(b) != 1 || b[0] != "for you" {
			t.Errorf("plain pull after a peeking wait = %q, want [for you] still unread", b)
		}
	})
}

func TestACancelledWaitIs503AndLeavesTheCursor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)

		// The client disconnecting, or the daemon stopping, both arrive as
		// the request's context being cancelled.
		ctx, cancel := context.WithCancel(t.Context())
		done := sending(ctx, h, "/pull", waitBody("reader", tenMinutes, ""))
		answered(done)
		say(t, h, "writer", "to the room")
		cancel()

		rec := answered(done)
		if rec == nil {
			t.Fatal("pull still waiting after its request was cancelled")
		}
		wantError(t, rec, http.StatusServiceUnavailable)

		plain := decode[server.PullResponse](t, post(t, h, "/pull", `{"workspace":"alpha","me":"reader"}`))
		if b := bodies(plain.Messages); len(b) != 1 || b[0] != "to the room" {
			t.Errorf("pull after a cancelled wait = %q, want [to the room]: a cancelled wait must not pull", b)
		}
	})
}

func TestWaitingOnAnUnknownMailboxIs404AtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, h := newServer(t)

		start := time.Now()
		rec := post(t, h, "/pull", waitBody("nobody", tenMinutes, ""))
		wantError(t, rec, http.StatusNotFound)
		if d := time.Since(start); d != 0 {
			t.Errorf("404 came after %v; a typo'd name must fail at once, not after the wait", d)
		}
	})
}

func TestAPullWithoutWaitMSNeverWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)

		start := time.Now()
		rec := post(t, h, "/pull", `{"workspace":"alpha","me":"reader"}`)
		wantStatus(t, rec, http.StatusOK)
		if d := time.Since(start); d != 0 {
			t.Errorf("a pull without wait_ms took %v", d)
		}
		if decode[server.PullResponse](t, rec).TimedOut {
			t.Error("timed_out = true for a pull that never waited")
		}
	})
}

func TestPullResponseFlattensTheEnvelope(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)

		rec := post(t, h, "/pull", waitBody("reader", `10`, ""))
		wantStatus(t, rec, http.StatusOK)

		// Embedding store.PullResult puts its fields beside timed_out, not
		// under a nested key.
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &keys); err != nil {
			t.Fatalf("decode: %v\nbody: %s", err, rec.Body.String())
		}
		for _, k := range []string{"messages", "latest_id", "unread_count", "timed_out"} {
			if _, ok := keys[k]; !ok {
				t.Errorf("response has no top-level %q: %s", k, rec.Body.String())
			}
		}
	})
}

package server_test

import (
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/eusean-tg/bro/internal/server"
)

func TestCloseReturnsTheIDOfTheControlMessage(t *testing.T) {
	st, h := newServer(t)
	room(t, st)

	rec := post(t, h, "/close", `{"workspace":"alpha","sender":"writer","body":"unit complete"}`)
	wantStatus(t, rec, http.StatusOK)
	id := decode[server.TellResponse](t, rec).ID

	msgs, err := st.Log(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != id || msgs[0].Kind != "control" {
		t.Errorf("stored %+v, want one control message with id %d", msgs, id)
	}
}

func TestCloseByAnUnregisteredSenderIs404(t *testing.T) {
	st, h := newServer(t)
	room(t, st)

	wantError(t, post(t, h, "/close", `{"workspace":"alpha","sender":"nobody","body":"x"}`), http.StatusNotFound)
}

func TestPullCarriesClosed(t *testing.T) {
	st, h := newServer(t)
	room(t, st)

	before := decode[server.PullResponse](t, post(t, h, "/pull", `{"workspace":"alpha","me":"reader"}`))
	if before.Closed {
		t.Error("closed = true before any close")
	}

	wantStatus(t, post(t, h, "/close", `{"workspace":"alpha","sender":"writer","body":"done"}`), http.StatusOK)

	after := decode[server.PullResponse](t, post(t, h, "/pull", `{"workspace":"alpha","me":"reader"}`))
	if !after.Closed {
		t.Error("closed = false after a close")
	}
}

func TestAWaitIsWokenByAClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)

		done := pulling(t.Context(), h, waitBody("reader", tenMinutes, ""))
		answered(done)
		wantStatus(t, post(t, h, "/close", `{"workspace":"alpha","sender":"writer","body":"done"}`), http.StatusOK)

		rec := answered(done)
		if rec == nil {
			t.Fatal("a waiting pull was not woken by a close; does /close publish?")
		}
		got := decode[server.PullResponse](t, rec)
		if !got.Closed || got.TimedOut {
			t.Errorf("closed = %v, timed_out = %v, want closed and not timed out", got.Closed, got.TimedOut)
		}
	})
}

func TestAClosedReaderWaitingAgainReturnsAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)
		wantStatus(t, post(t, h, "/close", `{"workspace":"alpha","sender":"writer","body":"done"}`), http.StatusOK)
		wantStatus(t, post(t, h, "/pull", `{"workspace":"alpha","me":"reader"}`), http.StatusOK)

		start := time.Now()
		got := decode[server.PullResponse](t, post(t, h, "/pull", waitBody("reader", tenMinutes, "")))
		if d := time.Since(start); d != 0 {
			t.Errorf("a closed reader's wait lasted %v, want none", d)
		}
		if !got.Closed {
			t.Error("closed = false")
		}
	})
}

package store_test

import (
	"errors"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

func closeWS(t *testing.T, s *store.Store, ws, sender string) int64 {
	t.Helper()

	id, err := s.CloseWorkspace(t.Context(), ws, sender, "unit complete")
	if err != nil {
		t.Fatalf("CloseWorkspace(%s by %s): %v", ws, sender, err)
	}
	return id
}

func pullClosed(t *testing.T, s *store.Store, ws, me string, opts store.PullOptions) store.PullResult {
	t.Helper()

	res, err := s.Pull(t.Context(), ws, me, opts)
	if err != nil {
		t.Fatalf("Pull(%s/%s): %v", ws, me, err)
	}
	return res
}

func TestCloseWritesAControlMessage(t *testing.T) {
	s := newStore(t)
	room(t, s)

	id, err := s.CloseWorkspace(t.Context(), "alpha", "writer", "all done")
	if err != nil {
		t.Fatalf("CloseWorkspace: %v", err)
	}

	msgs, err := s.Log(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Log returned %d messages, want the close", len(msgs))
	}
	m := msgs[0]
	if m.ID != id || m.Kind != "control" || m.Sender != "writer" || m.Body != "all done" {
		t.Errorf("stored %+v, want control message %d from writer saying 'all done'", m, id)
	}
}

func TestCloseByAnUnregisteredSenderIsErrNoMailbox(t *testing.T) {
	s := newStore(t)
	room(t, s)

	_, err := s.CloseWorkspace(t.Context(), "alpha", "nobody", "unit complete")
	if !errors.Is(err, store.ErrNoMailbox) {
		t.Fatalf("err = %v, want store.ErrNoMailbox", err)
	}
	if res := pullClosed(t, s, "alpha", "reader", store.PullOptions{}); res.Closed {
		t.Error("a rejected close still closed the workspace")
	}
	if msgs, _ := s.Log(t.Context(), "alpha"); len(msgs) != 0 {
		t.Errorf("a rejected close stored %d messages", len(msgs))
	}
}

func TestAnOpenWorkspaceIsNotClosed(t *testing.T) {
	s := newStore(t)
	room(t, s)
	tell(t, s, "alpha", "writer", "just chatting")

	if res := pullClosed(t, s, "alpha", "reader", store.PullOptions{}); res.Closed {
		t.Error("Closed = true in a workspace nobody closed")
	}
}

func TestPullReportsCloseToThoseRegisteredBeforeIt(t *testing.T) {
	s := newStore(t)
	room(t, s)
	id := closeWS(t, s, "alpha", "writer")

	res := pullClosed(t, s, "alpha", "reader", store.PullOptions{})
	if !res.Closed {
		t.Error("Closed = false for a mailbox registered before the close")
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != id {
		t.Errorf("messages = %+v, want the close message itself", res.Messages)
	}
}

func TestClosedStaysTrueAfterTheCloseIsRead(t *testing.T) {
	s := newStore(t)
	room(t, s)
	closeWS(t, s, "alpha", "writer")

	pullClosed(t, s, "alpha", "reader", store.PullOptions{})
	again := pullClosed(t, s, "alpha", "reader", store.PullOptions{})
	if !again.Closed {
		t.Error("Closed = false on the pull after the close was read; a closed worker must keep being told to stop")
	}
	if len(again.Messages) != 0 {
		t.Errorf("second pull returned %d messages, want 0", len(again.Messages))
	}
}

func TestClosedIsReportedOnPeekAndLastPulls(t *testing.T) {
	s := newStore(t)
	room(t, s)
	closeWS(t, s, "alpha", "writer")

	for _, opts := range []store.PullOptions{{Peek: true}, {Last: 1}} {
		if res := pullClosed(t, s, "alpha", "reader", opts); !res.Closed {
			t.Errorf("Pull(%+v).Closed = false after a close", opts)
		}
	}
}

func TestTheCloserIsNotClosedByItsOwnClose(t *testing.T) {
	s := newStore(t)
	room(t, s)
	closeWS(t, s, "alpha", "writer")

	if res := pullClosed(t, s, "alpha", "writer", store.PullOptions{}); res.Closed {
		t.Error("Closed = true for the mailbox that sent the close; the orchestrator carries on")
	}
}

func TestTheNextUnitStartsOpen(t *testing.T) {
	s := newStore(t)
	room(t, s)
	closeWS(t, s, "alpha", "writer")

	register(t, s, "alpha", "next-unit")
	if res := pullClosed(t, s, "alpha", "next-unit", store.PullOptions{}); res.Closed {
		t.Error("a mailbox registered after the close is closed; the next unit must start open")
	}
	if wakeable(t, s, "alpha", "next-unit") {
		t.Error("a close from before registration woke the next unit's worker")
	}
}

func TestASecondCloseEndsTheNextUnit(t *testing.T) {
	s := newStore(t)
	room(t, s)
	first := closeWS(t, s, "alpha", "writer")

	register(t, s, "alpha", "next-unit")
	second := closeWS(t, s, "alpha", "writer")
	if second <= first {
		t.Errorf("second close id %d, want a new message after %d", second, first)
	}

	for _, me := range []string{"reader", "next-unit"} {
		if res := pullClosed(t, s, "alpha", me, store.PullOptions{}); !res.Closed {
			t.Errorf("%s: Closed = false after the second close", me)
		}
	}
}

func TestTheCloserIsExemptOnlyFromItsOwnClose(t *testing.T) {
	s := newStore(t)
	room(t, s)
	closeWS(t, s, "alpha", "writer")
	closeWS(t, s, "alpha", "other")

	// writer registered before other's close and did not send it.
	if res := pullClosed(t, s, "alpha", "writer", store.PullOptions{}); !res.Closed {
		t.Error("writer is not closed by other's later close")
	}
}

func TestCloseIsScopedToTheWorkspace(t *testing.T) {
	s := newStore(t)
	room(t, s)
	register(t, s, "beta", "reader")
	closeWS(t, s, "alpha", "writer")

	if res := pullClosed(t, s, "beta", "reader", store.PullOptions{}); res.Closed {
		t.Error("closing alpha closed beta's reader")
	}
}

func TestAClosedMailboxIsAlwaysWakeable(t *testing.T) {
	s := newStore(t)
	room(t, s)
	closeWS(t, s, "alpha", "writer")

	if !wakeable(t, s, "alpha", "reader") {
		t.Error("Wakeable = false with an unread close")
	}

	// Having read the close, a worker that waits again must be sent
	// straight back to exit 4, not left waiting out its timeout.
	pullClosed(t, s, "alpha", "reader", store.PullOptions{})
	if !wakeable(t, s, "alpha", "reader") {
		t.Error("Wakeable = false for a closed mailbox that already read the close")
	}
}

func TestTheCloserIsNotWokenByItsOwnClose(t *testing.T) {
	s := newStore(t)
	room(t, s)
	closeWS(t, s, "alpha", "writer")

	if wakeable(t, s, "alpha", "writer") {
		t.Error("the closer was woken by its own close")
	}
}

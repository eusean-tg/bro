package store_test

import (
	"errors"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

func tell(t *testing.T, s *store.Store, ws, sender, body string, to ...string) {
	t.Helper()

	if _, err := s.Tell(t.Context(), ws, sender, body, to, store.KindNote); err != nil {
		t.Fatalf("Tell(%s from %s): %v", body, sender, err)
	}
}

func wakeable(t *testing.T, s *store.Store, ws, me string) bool {
	t.Helper()

	ok, err := s.Wakeable(t.Context(), ws, me)
	if err != nil {
		t.Fatalf("Wakeable(%s/%s): %v", ws, me, err)
	}
	return ok
}

func TestWakeableIsFalseInAQuietWorkspace(t *testing.T) {
	s := newStore(t)
	room(t, s)

	if wakeable(t, s, "alpha", "reader") {
		t.Error("Wakeable = true with no messages at all")
	}
}

func TestWakeableIsTrueForAnUnreadMention(t *testing.T) {
	s := newStore(t)
	room(t, s)
	tell(t, s, "alpha", "writer", "for you", "reader")

	if !wakeable(t, s, "alpha", "reader") {
		t.Error("Wakeable = false with an unread message naming the reader")
	}
}

func TestWakeableIgnoresAmbientMessages(t *testing.T) {
	s := newStore(t)
	room(t, s)
	tell(t, s, "alpha", "writer", "to the room")

	if wakeable(t, s, "alpha", "reader") {
		t.Error("Wakeable = true for a message that names nobody")
	}
}

func TestWakeableIgnoresMentionsOfSomeoneElse(t *testing.T) {
	s := newStore(t)
	room(t, s)
	tell(t, s, "alpha", "writer", "for other", "other")

	if wakeable(t, s, "alpha", "reader") {
		t.Error("Wakeable = true for a message naming other, not reader")
	}
}

func TestWakeableFindsTheReaderAmongSeveralRecipients(t *testing.T) {
	s := newStore(t)
	room(t, s)
	tell(t, s, "alpha", "writer", "for both", "other", "reader")

	if !wakeable(t, s, "alpha", "reader") {
		t.Error("Wakeable = false when reader is one of several recipients")
	}
}

func TestWakeableIgnoresTheReadersOwnMessages(t *testing.T) {
	s := newStore(t)
	room(t, s)
	tell(t, s, "alpha", "reader", "note to self", "reader")

	if wakeable(t, s, "alpha", "reader") {
		t.Error("Wakeable = true for a message the reader sent themselves")
	}
}

func TestWakeableIsFalseOnceAPullHasReadTheMention(t *testing.T) {
	s := newStore(t)
	room(t, s)
	tell(t, s, "alpha", "writer", "for you", "reader")

	if _, err := s.Pull(t.Context(), "alpha", "reader", store.PullOptions{}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	if wakeable(t, s, "alpha", "reader") {
		t.Error("Wakeable = true after a pull advanced the cursor past the mention")
	}
}

func TestWakeableStaysTrueAfterAPeek(t *testing.T) {
	s := newStore(t)
	room(t, s)
	tell(t, s, "alpha", "writer", "for you", "reader")

	if _, err := s.Pull(t.Context(), "alpha", "reader", store.PullOptions{Peek: true}); err != nil {
		t.Fatalf("Pull(peek): %v", err)
	}

	if !wakeable(t, s, "alpha", "reader") {
		t.Error("Wakeable = false after a peek; a peek leaves the mention unread")
	}
}

func TestWakeableIgnoresMentionsFromBeforeRegistration(t *testing.T) {
	s := newStore(t)
	register(t, s, "alpha", "writer")
	register(t, s, "alpha", "early")
	tell(t, s, "alpha", "writer", "before you joined", "early")

	// A mailbox registered now starts its cursor at the latest id, so the
	// earlier mention is history, not something to wake for.
	register(t, s, "alpha", "late")
	tell(t, s, "alpha", "writer", "ambient after joining")

	if wakeable(t, s, "alpha", "late") {
		t.Error("Wakeable = true for late, who has no mention after registering")
	}
}

func TestWakeableIsScopedToTheWorkspace(t *testing.T) {
	s := newStore(t)
	room(t, s)
	register(t, s, "beta", "writer")
	register(t, s, "beta", "reader")
	tell(t, s, "beta", "writer", "for beta's reader", "reader")

	if wakeable(t, s, "alpha", "reader") {
		t.Error("a mention of beta's reader woke alpha's reader")
	}
	if !wakeable(t, s, "beta", "reader") {
		t.Error("beta's reader was not woken by its own mention")
	}
}

func TestWakeableForAnUnknownMailboxIsErrNoMailbox(t *testing.T) {
	s := newStore(t)

	_, err := s.Wakeable(t.Context(), "alpha", "nobody")
	if !errors.Is(err, store.ErrNoMailbox) {
		t.Errorf("err = %v, want store.ErrNoMailbox: a wait for a typo'd name must fail, not block", err)
	}
}

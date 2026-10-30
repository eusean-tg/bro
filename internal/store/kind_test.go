package store_test

import (
	"errors"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

func control(t *testing.T, s *store.Store, ws, sender, body string, to ...string) {
	t.Helper()

	if _, err := s.Tell(t.Context(), ws, sender, body, to, store.KindControl); err != nil {
		t.Fatalf("Tell(control %s from %s): %v", body, sender, err)
	}
}

func TestTellStoresTheKind(t *testing.T) {
	s := newStore(t)
	room(t, s)
	tell(t, s, "alpha", "writer", "a note")
	control(t, s, "alpha", "writer", "a control")

	msgs, err := s.Log(t.Context(), "alpha", store.LogOptions{})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("Log returned %d messages, want 2", len(msgs))
	}
	if msgs[0].Kind != "note" || msgs[1].Kind != "control" {
		t.Errorf("kinds = %q, %q, want note, control", msgs[0].Kind, msgs[1].Kind)
	}
}

func TestTellRejectsAnUnknownKind(t *testing.T) {
	s := newStore(t)
	room(t, s)

	for _, kind := range []store.Kind{"", "contorl", "Note"} {
		_, err := s.Tell(t.Context(), "alpha", "writer", "bad kind", nil, kind)
		if !errors.Is(err, store.ErrBadKind) {
			t.Errorf("Tell(kind %q) err = %v, want store.ErrBadKind", kind, err)
		}
	}

	msgs, err := s.Log(t.Context(), "alpha", store.LogOptions{})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("a rejected kind stored %d messages", len(msgs))
	}
}

func TestPullCarriesTheKind(t *testing.T) {
	s := newStore(t)
	room(t, s)
	control(t, s, "alpha", "writer", "stop, main is broken")

	res, err := s.Pull(t.Context(), "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Kind != "control" {
		t.Errorf("messages = %+v, want one control message", res.Messages)
	}
}

func TestWakeableIsTrueForAnUnreadControlMessage(t *testing.T) {
	s := newStore(t)
	room(t, s)
	control(t, s, "alpha", "writer", "stop, main is broken")

	if !wakeable(t, s, "alpha", "reader") {
		t.Error("Wakeable = false for an unread control message; control messages wake everyone")
	}
}

func TestAControlMessageWakesEveryoneNotOnlyThoseNamed(t *testing.T) {
	s := newStore(t)
	room(t, s)
	control(t, s, "alpha", "writer", "for other, but everyone should wake", "other")

	if !wakeable(t, s, "alpha", "reader") {
		t.Error("a control message naming other did not wake reader")
	}
}

func TestWakeableIgnoresTheReadersOwnControlMessages(t *testing.T) {
	s := newStore(t)
	room(t, s)
	control(t, s, "alpha", "reader", "my own broadcast")

	if wakeable(t, s, "alpha", "reader") {
		t.Error("Wakeable = true for a control message the reader sent")
	}
}

func TestWakeableIsFalseOnceAControlMessageIsRead(t *testing.T) {
	s := newStore(t)
	room(t, s)
	control(t, s, "alpha", "writer", "stop, main is broken")

	if _, err := s.Pull(t.Context(), "alpha", "reader", store.PullOptions{}); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if wakeable(t, s, "alpha", "reader") {
		t.Error("Wakeable = true after a pull read past the control message")
	}
}

func TestWakeableIgnoresControlMessagesFromBeforeRegistration(t *testing.T) {
	s := newStore(t)
	register(t, s, "alpha", "writer")
	control(t, s, "alpha", "writer", "before you joined")
	register(t, s, "alpha", "late")

	if wakeable(t, s, "alpha", "late") {
		t.Error("Wakeable = true for a control message written before late registered")
	}
}

func TestControlWakesAreScopedToTheWorkspace(t *testing.T) {
	s := newStore(t)
	room(t, s)
	register(t, s, "beta", "writer")
	control(t, s, "beta", "writer", "beta only")

	if wakeable(t, s, "alpha", "reader") {
		t.Error("a control message in beta woke alpha's reader")
	}
}

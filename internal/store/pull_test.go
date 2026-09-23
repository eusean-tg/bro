package store_test

import (
	"errors"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

func TestPullReturnsMessagesAfterTheCursor(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	register(t, s, "alpha", "reader")
	register(t, s, "alpha", "writer")

	for _, body := range []string{"first", "second"} {
		if _, err := s.Tell(ctx, "alpha", "writer", body, nil); err != nil {
			t.Fatalf("Tell(%q): %v", body, err)
		}
	}

	res, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	msgs := res.Messages
	if len(msgs) != 2 {
		t.Fatalf("Pull returned %d messages, want 2", len(msgs))
	}
	if msgs[0].Body != "first" || msgs[1].Body != "second" {
		t.Errorf("Pull returned %q then %q, want first then second", msgs[0].Body, msgs[1].Body)
	}
}

func TestPullExcludesTheReadersOwnMessages(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	register(t, s, "alpha", "reader")
	register(t, s, "alpha", "writer")

	if _, err := s.Tell(ctx, "alpha", "reader", "my own announcement", nil); err != nil {
		t.Fatalf("Tell: %v", err)
	}
	if _, err := s.Tell(ctx, "alpha", "writer", "someone else's", nil); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	res, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	msgs := res.Messages
	if len(msgs) != 1 {
		t.Fatalf("Pull returned %d messages, want 1", len(msgs))
	}
	if msgs[0].Body != "someone else's" {
		t.Errorf("Body = %q, want the other sender's message", msgs[0].Body)
	}
}

func TestPullAdvancesTheCursorSoASecondPullIsEmpty(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	register(t, s, "alpha", "reader")
	register(t, s, "alpha", "writer")

	if _, err := s.Tell(ctx, "alpha", "writer", "only message", nil); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	res, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("first Pull: %v", err)
	}
	first := res.Messages
	if len(first) != 1 {
		t.Fatalf("first Pull returned %d messages, want 1", len(first))
	}

	res, err = s.Pull(ctx, "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("second Pull: %v", err)
	}
	second := res.Messages
	if len(second) != 0 {
		t.Errorf("second Pull returned %d messages, want 0", len(second))
	}
}

func TestPullAdvancesPastTheReadersOwnMessages(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	register(t, s, "alpha", "reader")

	// The reader is the only sender, so Pull returns nothing — but the cursor
	// must still move past the message. A cursor that stalls on the reader's
	// own traffic never reports the mailbox as caught up.
	id, err := s.Tell(ctx, "alpha", "reader", "my own announcement", nil)
	if err != nil {
		t.Fatalf("Tell: %v", err)
	}

	if _, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	mb, err := s.Mailbox(ctx, "alpha", "reader")
	if err != nil {
		t.Fatalf("Mailbox: %v", err)
	}
	if mb.Cursor != id {
		t.Errorf("Cursor = %d, want %d: everything up to here has been seen", mb.Cursor, id)
	}
}

func TestPullIgnoresTrafficFromBeforeRegistration(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	register(t, s, "alpha", "writer")

	for _, body := range []string{"before 1", "before 2"} {
		if _, err := s.Tell(ctx, "alpha", "writer", body, nil); err != nil {
			t.Fatalf("Tell: %v", err)
		}
	}

	register(t, s, "alpha", "latecomer")

	if _, err := s.Tell(ctx, "alpha", "writer", "after", nil); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	res, err := s.Pull(ctx, "alpha", "latecomer", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	msgs := res.Messages
	if len(msgs) != 1 {
		t.Fatalf("Pull returned %d messages, want 1", len(msgs))
	}
	if msgs[0].Body != "after" {
		t.Errorf("Body = %q, want %q", msgs[0].Body, "after")
	}
}

func TestPullIsScopedToOneWorkspace(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	register(t, s, "alpha", "reader")
	register(t, s, "beta", "writer")

	if _, err := s.Tell(ctx, "beta", "writer", "belongs to beta", nil); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	res, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	msgs := res.Messages
	if len(msgs) != 0 {
		t.Errorf("Pull returned %d messages, want 0", len(msgs))
	}
}

func TestPullRejectsAnUnknownMailbox(t *testing.T) {
	s := newStore(t)

	if _, err := s.Pull(t.Context(), "alpha", "ghost", store.PullOptions{}); !errors.Is(err, store.ErrNoMailbox) {
		t.Fatalf("Pull error = %v, want ErrNoMailbox", err)
	}
}

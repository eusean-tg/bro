package store_test

import (
	"errors"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

// room registers three mailboxes in alpha: reader, other, and writer.
func room(t *testing.T, s *store.Store) {
	t.Helper()
	for _, name := range []string{"reader", "other", "writer"} {
		register(t, s, "alpha", name)
	}
}

func TestPullMarksAMessageAddressedToTheReader(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	room(t, s)

	if _, err := s.Tell(ctx, "alpha", "writer", "for you", []string{"reader"}); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	msgs, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Pull returned %d messages, want 1", len(msgs))
	}
	if !msgs[0].Mentioned {
		t.Error("Mentioned = false, want true: the message names this mailbox")
	}
}

func TestPullDoesNotMarkAMessageAddressedToSomeoneElse(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	room(t, s)

	if _, err := s.Tell(ctx, "alpha", "writer", "for other", []string{"other"}); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	msgs, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Pull returned %d messages, want 1: the room sees everything", len(msgs))
	}
	if msgs[0].Mentioned {
		t.Error("Mentioned = true, want false: this message names a different mailbox")
	}
}

func TestPullMarksAMessageAddressedToSeveralMailboxes(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	room(t, s)

	if _, err := s.Tell(ctx, "alpha", "writer", "for both", []string{"reader", "other"}); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	for _, me := range []string{"reader", "other"} {
		msgs, err := s.Pull(ctx, "alpha", me, store.PullOptions{})
		if err != nil {
			t.Fatalf("Pull(%s): %v", me, err)
		}
		if len(msgs) != 1 || !msgs[0].Mentioned {
			t.Errorf("Pull(%s) did not mark the message as mentioned", me)
		}
	}
}

func TestPullLeavesAnUnaddressedMessageUnmarked(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	room(t, s)

	if _, err := s.Tell(ctx, "alpha", "writer", "to the room", nil); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	msgs, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Pull returned %d messages, want 1", len(msgs))
	}
	if msgs[0].Mentioned {
		t.Error("Mentioned = true, want false: no rows means to the room")
	}
}

func TestTellRejectsAnUnknownRecipient(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	room(t, s)

	if _, err := s.Tell(ctx, "alpha", "writer", "hi", []string{"ghost"}); !errors.Is(err, store.ErrNoMailbox) {
		t.Fatalf("Tell error = %v, want ErrNoMailbox", err)
	}
}

func TestTellStoresNothingWhenOneRecipientIsUnknown(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	room(t, s)

	// reader exists, ghost does not. The message must not survive.
	if _, err := s.Tell(ctx, "alpha", "writer", "half addressed", []string{"reader", "ghost"}); err == nil {
		t.Fatal("Tell succeeded with an unknown recipient, want an error")
	}

	msgs, err := s.Log(ctx, "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("stored %d messages, want 0: the whole tell rolls back", len(msgs))
	}
}

func TestTellRejectsARecipientFromAnotherWorkspace(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	room(t, s)
	register(t, s, "beta", "elsewhere")

	if _, err := s.Tell(ctx, "alpha", "writer", "hi", []string{"elsewhere"}); !errors.Is(err, store.ErrNoMailbox) {
		t.Fatalf("Tell error = %v, want ErrNoMailbox", err)
	}
}

func TestLogLeavesMentionedUnset(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	room(t, s)

	if _, err := s.Tell(ctx, "alpha", "writer", "for you", []string{"reader"}); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	msgs, err := s.Log(ctx, "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Log returned %d messages, want 1", len(msgs))
	}
	if msgs[0].Mentioned {
		t.Error("Mentioned = true, want false: a transcript has no reader")
	}
}

package store_test

import (
	"errors"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

func TestRegisterReturnsTheMailbox(t *testing.T) {
	s := newStore(t)

	mb, err := s.Register(t.Context(), "alpha", "worker-1", "handles auth")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if mb.Name != "worker-1" {
		t.Errorf("Name = %q, want %q", mb.Name, "worker-1")
	}
	if mb.Description != "handles auth" {
		t.Errorf("Description = %q, want %q", mb.Description, "handles auth")
	}
	if mb.CreatedAt != testClock.Unix() {
		t.Errorf("CreatedAt = %d, want %d", mb.CreatedAt, testClock.Unix())
	}
}

func TestRegisterInEmptyWorkspaceStartsCursorAtZero(t *testing.T) {
	s := newStore(t)

	mb, err := s.Register(t.Context(), "alpha", "worker-1", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if mb.Cursor != 0 {
		t.Errorf("Cursor = %d, want 0 in a workspace with no messages", mb.Cursor)
	}
}

func TestRegisterStartsCursorAtTheWorkspaceHighWaterMark(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	if _, err := s.Register(ctx, "alpha", "early", ""); err != nil {
		t.Fatalf("Register(early): %v", err)
	}

	var last int64
	for _, body := range []string{"first", "second", "third"} {
		id, err := s.Tell(ctx, "alpha", "early", body, nil)
		if err != nil {
			t.Fatalf("Tell(%q): %v", body, err)
		}
		last = id
	}

	// A mailbox registered now must not see any of the three.
	late, err := s.Register(ctx, "alpha", "late", "")
	if err != nil {
		t.Fatalf("Register(late): %v", err)
	}
	if late.Cursor != last {
		t.Errorf("Cursor = %d, want %d (the highest id in the workspace)", late.Cursor, last)
	}
}

func TestRegisterIgnoresOtherWorkspacesWhenSettingTheCursor(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	if _, err := s.Register(ctx, "beta", "noisy", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for _, body := range []string{"b1", "b2", "b3"} {
		if _, err := s.Tell(ctx, "beta", "noisy", body, nil); err != nil {
			t.Fatalf("Tell: %v", err)
		}
	}

	mb, err := s.Register(ctx, "alpha", "worker-1", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if mb.Cursor != 0 {
		t.Errorf("Cursor = %d, want 0: alpha has no messages of its own", mb.Cursor)
	}
}

func TestRegisterRejectsADuplicateName(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	if _, err := s.Register(ctx, "alpha", "worker-1", "first"); err != nil {
		t.Fatalf("first Register: %v", err)
	}

	if _, err := s.Register(ctx, "alpha", "worker-1", "second"); !errors.Is(err, store.ErrMailboxExists) {
		t.Fatalf("second Register error = %v, want ErrMailboxExists", err)
	}

	// The original must survive the rejected attempt.
	mb, err := s.Mailbox(ctx, "alpha", "worker-1")
	if err != nil {
		t.Fatalf("Mailbox: %v", err)
	}
	if mb.Description != "first" {
		t.Errorf("Description = %q, want %q", mb.Description, "first")
	}
}

func TestRegisterAllowsTheSameNameInAnotherWorkspace(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	if _, err := s.Register(ctx, "alpha", "worker-1", ""); err != nil {
		t.Fatalf("Register(alpha): %v", err)
	}
	if _, err := s.Register(ctx, "beta", "worker-1", ""); err != nil {
		t.Fatalf("Register(beta): %v", err)
	}
}

func TestMailboxReportsAnUnknownName(t *testing.T) {
	s := newStore(t)

	if _, err := s.Mailbox(t.Context(), "alpha", "nobody"); !errors.Is(err, store.ErrNoMailbox) {
		t.Fatalf("Mailbox error = %v, want ErrNoMailbox", err)
	}
}

func TestTellRejectsAnUnregisteredSender(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	if _, err := s.Tell(ctx, "alpha", "ghost", "who am i", nil); !errors.Is(err, store.ErrNoMailbox) {
		t.Fatalf("Tell error = %v, want ErrNoMailbox", err)
	}

	msgs, err := s.Log(ctx, "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("stored %d messages, want 0", len(msgs))
	}
}

func TestTellRejectsASenderRegisteredElsewhere(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	if _, err := s.Register(ctx, "beta", "worker-1", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := s.Tell(ctx, "alpha", "worker-1", "wrong room", nil); !errors.Is(err, store.ErrNoMailbox) {
		t.Fatalf("Tell error = %v, want ErrNoMailbox", err)
	}
}

package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/eusean-tg/bro/internal/store"
)

// testClock is the fixed time every Store in this file reports. Any non-zero
// instant would do; a recognisable one makes a failure message readable.
var testClock = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

// newStore opens a Store on its own database file with the clock pinned.
func newStore(t *testing.T) *store.Store {
	t.Helper()

	s, err := store.Open(filepath.Join(t.TempDir(), "bro.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	s.Now = func() time.Time { return testClock }
	return s
}

// register creates a mailbox so that Tell will accept it as a sender.
func register(t *testing.T, s *store.Store, ws, name string) {
	t.Helper()
	if _, err := s.Register(t.Context(), ws, name, ""); err != nil {
		t.Fatalf("Register(%s/%s): %v", ws, name, err)
	}
}

func TestOpenAppliesSchemaAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bro.db")

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopening an existing database must not fail on tables that are
	// already there.
	s, err = store.Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpenRejectsUnusablePath(t *testing.T) {
	// A directory that does not exist cannot hold a database file. Open must
	// report that, rather than returning a Store that fails on first use.
	path := filepath.Join(t.TempDir(), "no-such-dir", "bro.db")

	s, err := store.Open(path)
	if err == nil {
		s.Close()
		t.Fatal("Open succeeded on an unwritable path, want error")
	}
}

func TestTellReturnsIncreasingID(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	register(t, s, "ws", "worker-1")
	register(t, s, "ws", "worker-2")

	first, err := s.Tell(ctx, "ws", "worker-1", "started on auth", nil, store.KindNote)
	if err != nil {
		t.Fatalf("Tell: %v", err)
	}
	second, err := s.Tell(ctx, "ws", "worker-2", "started on storage", nil, store.KindNote)
	if err != nil {
		t.Fatalf("Tell: %v", err)
	}

	if first <= 0 {
		t.Errorf("first id = %d, want a positive value", first)
	}
	if second <= first {
		t.Errorf("second id = %d, want greater than first id %d", second, first)
	}
}

func TestLogReturnsMessagesInIDOrder(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	register(t, s, "ws", "worker-1")

	bodies := []string{"first", "second", "third"}
	for _, body := range bodies {
		if _, err := s.Tell(ctx, "ws", "worker-1", body, nil, store.KindNote); err != nil {
			t.Fatalf("Tell(%q): %v", body, err)
		}
	}

	msgs, err := s.Log(ctx, "ws", store.LogOptions{})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != len(bodies) {
		t.Fatalf("Log returned %d messages, want %d", len(msgs), len(bodies))
	}

	for i, want := range bodies {
		if msgs[i].Body != want {
			t.Errorf("msgs[%d].Body = %q, want %q", i, msgs[i].Body, want)
		}
		if i > 0 && msgs[i].ID <= msgs[i-1].ID {
			t.Errorf("msgs[%d].ID = %d, not greater than previous %d",
				i, msgs[i].ID, msgs[i-1].ID)
		}
	}
}

func TestTellRecordsSenderKindAndInjectedTime(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	register(t, s, "ws", "worker-3")

	if _, err := s.Tell(ctx, "ws", "worker-3", "refactored auth middleware", nil, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	msgs, err := s.Log(ctx, "ws", store.LogOptions{})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Log returned %d messages, want 1", len(msgs))
	}

	got := msgs[0]
	if got.Sender != "worker-3" {
		t.Errorf("Sender = %q, want %q", got.Sender, "worker-3")
	}
	if got.Body != "refactored auth middleware" {
		t.Errorf("Body = %q, want %q", got.Body, "refactored auth middleware")
	}
	// Tell writes plain notes. 'control' arrives with bro close, in M5.
	if got.Kind != "note" {
		t.Errorf("Kind = %q, want %q", got.Kind, "note")
	}
	// The clock is a struct field precisely so this assertion can exist.
	if got.CreatedAt != testClock.Unix() {
		t.Errorf("CreatedAt = %d, want %d", got.CreatedAt, testClock.Unix())
	}
}

func TestLogIsScopedToOneWorkspace(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	register(t, s, "alpha", "worker-1")
	register(t, s, "beta", "worker-2")

	if _, err := s.Tell(ctx, "alpha", "worker-1", "in alpha", nil, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}
	if _, err := s.Tell(ctx, "beta", "worker-2", "in beta", nil, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	msgs, err := s.Log(ctx, "alpha", store.LogOptions{})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Log(alpha) returned %d messages, want 1", len(msgs))
	}
	if msgs[0].Body != "in alpha" {
		t.Errorf("Body = %q, want %q", msgs[0].Body, "in alpha")
	}
}

func TestLogOnEmptyWorkspaceReturnsNoMessagesAndNoError(t *testing.T) {
	s := newStore(t)

	msgs, err := s.Log(t.Context(), "never-used", store.LogOptions{})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("Log returned %d messages, want 0", len(msgs))
	}
}

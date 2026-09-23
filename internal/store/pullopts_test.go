package store_test

import (
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

// seedWorkspace registers reader and writer in ws, then sends bodies from
// writer, returning the id of each message in order.
func seedWorkspace(t *testing.T, s *store.Store, ws string, bodies ...string) []int64 {
	t.Helper()
	register(t, s, ws, "reader")
	register(t, s, ws, "writer")

	ids := make([]int64, 0, len(bodies))
	for _, body := range bodies {
		id, err := s.Tell(t.Context(), ws, "writer", body, nil)
		if err != nil {
			t.Fatalf("Tell(%q): %v", body, err)
		}
		ids = append(ids, id)
	}
	return ids
}

func bodies(msgs []store.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Body
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPullWithPeekReturnsTheSameMessagesTwice(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedWorkspace(t, s, "alpha", "first", "second")

	opts := store.PullOptions{Peek: true}

	res, err := s.Pull(ctx, "alpha", "reader", opts)
	if err != nil {
		t.Fatalf("first Pull: %v", err)
	}
	first := res.Messages
	res, err = s.Pull(ctx, "alpha", "reader", opts)
	if err != nil {
		t.Fatalf("second Pull: %v", err)
	}
	second := res.Messages

	if !equal(bodies(first), []string{"first", "second"}) {
		t.Fatalf("first peek = %v, want [first second]", bodies(first))
	}
	if !equal(bodies(second), bodies(first)) {
		t.Errorf("second peek = %v, want the same as the first %v", bodies(second), bodies(first))
	}
}

func TestPullWithPeekLeavesTheCursorWhereItWas(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedWorkspace(t, s, "alpha", "first")

	if _, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{Peek: true}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	mb, err := s.Mailbox(ctx, "alpha", "reader")
	if err != nil {
		t.Fatalf("Mailbox: %v", err)
	}
	if mb.Cursor != 0 {
		t.Errorf("Cursor = %d, want 0: a peek reads without consuming", mb.Cursor)
	}
}

func TestPullWithSinceIgnoresTheCursor(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	ids := seedWorkspace(t, s, "alpha", "first", "second", "third")

	// Consume everything so the cursor is at the end.
	if _, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	since := ids[0]
	res, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{Since: &since})
	if err != nil {
		t.Fatalf("Pull(since): %v", err)
	}
	msgs := res.Messages
	if !equal(bodies(msgs), []string{"second", "third"}) {
		t.Errorf("Pull(since=%d) = %v, want [second third]", since, bodies(msgs))
	}
}

func TestPullWithSinceZeroReturnsTheWholeWorkspace(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedWorkspace(t, s, "alpha", "first", "second")

	if _, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	// Zero is a real starting point, distinct from "not specified".
	var since int64
	res, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{Since: &since})
	if err != nil {
		t.Fatalf("Pull(since=0): %v", err)
	}
	msgs := res.Messages
	if !equal(bodies(msgs), []string{"first", "second"}) {
		t.Errorf("Pull(since=0) = %v, want [first second]", bodies(msgs))
	}
}

func TestPullWithSinceDoesNotAdvanceTheCursor(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedWorkspace(t, s, "alpha", "first", "second")

	var since int64
	if _, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{Since: &since}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	mb, err := s.Mailbox(ctx, "alpha", "reader")
	if err != nil {
		t.Fatalf("Mailbox: %v", err)
	}
	if mb.Cursor != 0 {
		t.Errorf("Cursor = %d, want 0: an explicit range is a read", mb.Cursor)
	}
}

func TestPullWithLastReturnsTheMostRecentInIDOrder(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedWorkspace(t, s, "alpha", "first", "second", "third", "fourth")

	res, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{Last: 2})
	if err != nil {
		t.Fatalf("Pull(last=2): %v", err)
	}
	msgs := res.Messages
	// The two newest, still oldest-first.
	if !equal(bodies(msgs), []string{"third", "fourth"}) {
		t.Errorf("Pull(last=2) = %v, want [third fourth]", bodies(msgs))
	}
}

func TestPullWithLastIgnoresTheCursorAndLeavesIt(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedWorkspace(t, s, "alpha", "first", "second")

	if _, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	res, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{Last: 1})
	if err != nil {
		t.Fatalf("Pull(last=1): %v", err)
	}
	msgs := res.Messages
	if !equal(bodies(msgs), []string{"second"}) {
		t.Errorf("Pull(last=1) = %v, want [second] even though the cursor is at the end", bodies(msgs))
	}

	mb, err := s.Mailbox(ctx, "alpha", "reader")
	if err != nil {
		t.Fatalf("Mailbox: %v", err)
	}
	if mb.Cursor != 2 {
		t.Errorf("Cursor = %d, want 2: --last must not move it", mb.Cursor)
	}
}

func TestPullWithLastLargerThanTheWorkspaceReturnsEverything(t *testing.T) {
	s := newStore(t)
	seedWorkspace(t, s, "alpha", "first", "second")

	res, err := s.Pull(t.Context(), "alpha", "reader", store.PullOptions{Last: 100})
	if err != nil {
		t.Fatalf("Pull(last=100): %v", err)
	}
	msgs := res.Messages
	if !equal(bodies(msgs), []string{"first", "second"}) {
		t.Errorf("Pull(last=100) = %v, want [first second]", bodies(msgs))
	}
}

func TestPullWithOptionsStillExcludesTheReadersOwnMessages(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedWorkspace(t, s, "alpha", "from writer")

	if _, err := s.Tell(ctx, "alpha", "reader", "from reader", nil); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	var since int64
	for _, opts := range []store.PullOptions{
		{Peek: true},
		{Since: &since},
		{Last: 10},
	} {
		res, err := s.Pull(ctx, "alpha", "reader", opts)
		if err != nil {
			t.Fatalf("Pull(%+v): %v", opts, err)
		}
		msgs := res.Messages
		if !equal(bodies(msgs), []string{"from writer"}) {
			t.Errorf("Pull(%+v) = %v, want [from writer]", opts, bodies(msgs))
		}
	}
}

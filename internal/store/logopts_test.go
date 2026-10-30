package store_test

import (
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

// logBodies reads ws's log with opts and returns the bodies.
func logBodies(t *testing.T, s *store.Store, ws string, opts store.LogOptions) []string {
	t.Helper()

	msgs, err := s.Log(t.Context(), ws, opts)
	if err != nil {
		t.Fatalf("Log(%s, %+v): %v", ws, opts, err)
	}
	return bodies(msgs)
}

func TestLogAfterReturnsOnlyLaterMessages(t *testing.T) {
	s := newStore(t)
	ids := seedWorkspace(t, s, "alpha", "first", "second", "third")

	if got, want := logBodies(t, s, "alpha", store.LogOptions{After: ids[0]}), []string{"second", "third"}; !equal(got, want) {
		t.Errorf("Log after the first = %q, want %q", got, want)
	}
	if got := logBodies(t, s, "alpha", store.LogOptions{After: ids[2]}); len(got) != 0 {
		t.Errorf("Log after the latest = %q, want nothing", got)
	}
}

func TestLogLastReturnsTheNewestOldestFirst(t *testing.T) {
	s := newStore(t)
	seedWorkspace(t, s, "alpha", "first", "second", "third", "fourth")

	if got, want := logBodies(t, s, "alpha", store.LogOptions{Last: 2}), []string{"third", "fourth"}; !equal(got, want) {
		t.Errorf("Log with Last 2 = %q, want %q: the newest two, oldest first", got, want)
	}
	if got, want := logBodies(t, s, "alpha", store.LogOptions{Last: 10}), []string{"first", "second", "third", "fourth"}; !equal(got, want) {
		t.Errorf("Log with Last 10 of 4 = %q, want all of them, %q", got, want)
	}
}

func TestLogAfterAndLastTakeTheNewestOfWhatIsAfter(t *testing.T) {
	s := newStore(t)
	ids := seedWorkspace(t, s, "alpha", "first", "second", "third", "fourth")

	if got, want := logBodies(t, s, "alpha", store.LogOptions{After: ids[0], Last: 2}), []string{"third", "fourth"}; !equal(got, want) {
		t.Errorf("Log after the first with Last 2 = %q, want %q", got, want)
	}
	if got, want := logBodies(t, s, "alpha", store.LogOptions{After: ids[2], Last: 2}), []string{"fourth"}; !equal(got, want) {
		t.Errorf("Log after the third with Last 2 = %q, want %q: Last never reaches back past After", got, want)
	}
}

// Ids come from one sequence shared by every workspace, so the ids after a
// given one include other workspaces' messages.
func TestLogAfterAndLastStayInTheWorkspace(t *testing.T) {
	s := newStore(t)
	ids := seedWorkspace(t, s, "alpha", "alpha first")
	seedWorkspace(t, s, "beta", "beta first", "beta second")
	if _, err := s.Tell(t.Context(), "alpha", "writer", "alpha second", nil, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	if got, want := logBodies(t, s, "alpha", store.LogOptions{After: ids[0]}), []string{"alpha second"}; !equal(got, want) {
		t.Errorf("Log(alpha) after its first = %q, want %q", got, want)
	}
	if got, want := logBodies(t, s, "alpha", store.LogOptions{Last: 2}), []string{"alpha first", "alpha second"}; !equal(got, want) {
		t.Errorf("Log(alpha) with Last 2 = %q, want %q", got, want)
	}
}

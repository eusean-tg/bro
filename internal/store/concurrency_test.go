package store_test

import (
	"sync"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

// The daemon serves every request on its own goroutine against one Store, so
// tells, pulls and wake checks overlap. None of them may fail because another
// happened to be running: SQLite's "database is locked" is a scheduling
// accident, not an answer to give a caller.
func TestOverlappingReadsAndWritesAllSucceed(t *testing.T) {
	s := newStore(t)
	room(t, s)

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, 4*n)
	for range n {
		wg.Add(4)
		go func() {
			defer wg.Done()
			_, err := s.Tell(t.Context(), "alpha", "writer", "for you", []string{"reader"})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := s.Pull(t.Context(), "alpha", "reader", store.PullOptions{})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := s.Pull(t.Context(), "alpha", "other", store.PullOptions{})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := s.Wakeable(t.Context(), "alpha", "reader")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	failed := 0
	for err := range errs {
		if err != nil {
			if failed == 0 {
				t.Errorf("an overlapping call failed: %v", err)
			}
			failed++
		}
	}
	if failed > 1 {
		t.Errorf("%d of %d overlapping calls failed in all", failed, 4*n)
	}
}

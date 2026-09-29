package store_test

import (
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

func TestPullReportsTheWorkspaceHead(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	ids := seedWorkspace(t, s, "alpha", "first", "second", "third")

	res, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.LatestID != ids[len(ids)-1] {
		t.Errorf("LatestID = %d, want %d (the highest id in the workspace)", res.LatestID, ids[len(ids)-1])
	}
}

func TestPullReportsTheWorkspaceHeadEvenWhenTheReaderSentItselfTheNewest(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedWorkspace(t, s, "alpha", "from writer")

	id, err := s.Tell(ctx, "alpha", "reader", "my own", nil, store.KindNote)
	if err != nil {
		t.Fatalf("Tell: %v", err)
	}

	res, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.LatestID != id {
		t.Errorf("LatestID = %d, want %d: the head counts every message", res.LatestID, id)
	}
}

func TestPullLeavesNothingUnreadAfterAPlainPull(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedWorkspace(t, s, "alpha", "first", "second")

	res, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.UnreadCount != 0 {
		t.Errorf("UnreadCount = %d, want 0 after consuming everything", res.UnreadCount)
	}
}

func TestPullWithPeekReportsWhatIsStillWaiting(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedWorkspace(t, s, "alpha", "first", "second", "third")

	res, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{Peek: true})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.UnreadCount != 3 {
		t.Errorf("UnreadCount = %d, want 3: a peek does not consume", res.UnreadCount)
	}
}

func TestPullUnreadCountExcludesTheReadersOwnMessages(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	seedWorkspace(t, s, "alpha", "from writer")

	if _, err := s.Tell(ctx, "alpha", "reader", "my own", nil, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	res, err := s.Pull(ctx, "alpha", "reader", store.PullOptions{Peek: true})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.UnreadCount != 1 {
		t.Errorf("UnreadCount = %d, want 1: only the other sender's message is unread", res.UnreadCount)
	}
}

func TestPullReturnsAnEmptySliceRatherThanNil(t *testing.T) {
	s := newStore(t)
	register(t, s, "alpha", "reader")

	res, err := s.Pull(t.Context(), "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	// A nil slice marshals as null; an empty one as []. Clients parsing the
	// JSON must see one type regardless of whether anyone has spoken.
	if res.Messages == nil {
		t.Error("Messages is nil, want an empty slice")
	}
}

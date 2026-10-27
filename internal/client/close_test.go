package client_test

import (
	"errors"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

func TestCloseWorkspaceClosesItForEarlierMailboxes(t *testing.T) {
	st, c := newClient(t)
	register(t, st, "alpha", "writer")
	register(t, st, "alpha", "reader")

	id, err := c.CloseWorkspace(t.Context(), "alpha", "writer", "unit complete")
	if err != nil {
		t.Fatalf("CloseWorkspace: %v", err)
	}

	res, err := c.Pull(t.Context(), "alpha", "reader", store.PullOptions{}, 0)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if !res.Closed {
		t.Error("Closed = false after CloseWorkspace")
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != id {
		t.Errorf("messages = %+v, want the close message %d", res.Messages, id)
	}
}

func TestCloseWorkspaceByAnUnknownSenderIsErrNoMailbox(t *testing.T) {
	_, c := newClient(t)

	if _, err := c.CloseWorkspace(t.Context(), "alpha", "nobody", "x"); !errors.Is(err, store.ErrNoMailbox) {
		t.Errorf("err = %v, want store.ErrNoMailbox", err)
	}
}

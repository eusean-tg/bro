package client_test

import (
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

func TestTellSendsTheKind(t *testing.T) {
	st, c := newClient(t)
	register(t, st, "alpha", "writer")

	if _, err := c.Tell(t.Context(), "alpha", "writer", "stop", nil, store.KindControl); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	msgs, err := st.Log(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Kind != "control" {
		t.Errorf("stored %+v, want one control message", msgs)
	}
}

package client_test

import "testing"

func TestMailboxesReturnsTheRoster(t *testing.T) {
	st, c := newClient(t)
	if _, err := st.Register(t.Context(), "alpha", "writer", "owns the parser"); err != nil {
		t.Fatalf("Register(writer): %v", err)
	}
	register(t, st, "alpha", "orchestrator")

	got, err := c.Mailboxes(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Mailboxes: %v", err)
	}
	if len(got) != 2 ||
		got[0].Name != "orchestrator" ||
		got[1].Name != "writer" ||
		got[1].Description != "owns the parser" {
		t.Errorf("mailboxes = %+v, want orchestrator, then writer with its description", got)
	}
}

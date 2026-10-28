package store_test

import "testing"

func TestMailboxesListsTheWorkspaceByName(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	// Neither registration order nor its reverse is name order.
	for _, mb := range [][3]string{ // {workspace, name, description}
		{"alpha", "writer", "owns the parser"},
		{"beta", "elsewhere", ""},
		{"alpha", "lexer", ""},
		{"alpha", "orchestrator", "coordinates the unit"},
	} {
		if _, err := s.Register(ctx, mb[0], mb[1], mb[2]); err != nil {
			t.Fatalf("Register(%s/%s): %v", mb[0], mb[1], err)
		}
	}

	got, err := s.Mailboxes(ctx, "alpha")
	if err != nil {
		t.Fatalf("Mailboxes: %v", err)
	}

	var names []string
	for _, mb := range got {
		names = append(names, mb.Name)
	}
	if want := []string{"lexer", "orchestrator", "writer"}; !equal(names, want) {
		t.Fatalf("names = %v, want alpha's mailboxes by name %v", names, want)
	}
	if got[2].Description != "owns the parser" {
		t.Errorf("writer's description = %q, want %q", got[2].Description, "owns the parser")
	}
}

func TestMailboxesOfAnEmptyWorkspaceIsEmptyNotNil(t *testing.T) {
	s := newStore(t)

	got, err := s.Mailboxes(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Mailboxes: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("Mailboxes = %#v, want an empty, non-nil slice: nil encodes as JSON null", got)
	}
}

package main

import (
	"bytes"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

func tellKind(t *testing.T, args ...string) (kinds []string, err error) {
	t.Helper()

	s := seeded(t)
	cfg := Config{Identity: "worker-1", Workspace: "alpha"}
	if _, err := s.Register(t.Context(), cfg.Workspace, cfg.Identity, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	var out bytes.Buffer
	err = runTell(t.Context(), clientFor(t, s), cfg, &out, args)

	msgs, lerr := s.Log(t.Context(), "alpha", store.LogOptions{})
	if lerr != nil {
		t.Fatalf("Log: %v", lerr)
	}
	for _, m := range msgs {
		kinds = append(kinds, m.Kind)
	}
	return kinds, err
}

func TestRunTellDefaultsToANote(t *testing.T) {
	kinds, err := tellKind(t, "hello")
	if err != nil {
		t.Fatalf("runTell: %v", err)
	}
	if len(kinds) != 1 || kinds[0] != "note" {
		t.Errorf("kinds = %q, want [note]", kinds)
	}
}

func TestRunTellKindControl(t *testing.T) {
	kinds, err := tellKind(t, "--kind", "control", "stop, main is broken")
	if err != nil {
		t.Fatalf("runTell --kind control: %v", err)
	}
	if len(kinds) != 1 || kinds[0] != "control" {
		t.Errorf("kinds = %q, want [control]", kinds)
	}
}

func TestRunTellRejectsAnUnknownKind(t *testing.T) {
	kinds, err := tellKind(t, "--kind", "contorl", "typo")
	if err == nil {
		t.Error("runTell --kind contorl succeeded, want an error")
	}
	if len(kinds) != 0 {
		t.Errorf("a rejected kind stored %q", kinds)
	}
}

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

func TestRunRegisterUsesTheGivenName(t *testing.T) {
	s := seeded(t)
	cfg := Config{Workspace: "alpha"}

	var out bytes.Buffer
	if err := runRegister(t.Context(), clientFor(t, s), cfg, &out, []string{"-n", "worker-1", "-d", "handles auth"}); err != nil {
		t.Fatalf("runRegister: %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != "worker-1" {
		t.Errorf("wrote %q, want the registered name %q", got, "worker-1")
	}

	mb, err := s.Mailbox(t.Context(), "alpha", "worker-1")
	if err != nil {
		t.Fatalf("Mailbox: %v", err)
	}
	if mb.Description != "handles auth" {
		t.Errorf("Description = %q, want %q", mb.Description, "handles auth")
	}
}

func TestRunRegisterFallsBackToTheResolvedIdentity(t *testing.T) {
	s := seeded(t)
	cfg := Config{Identity: "orchestrator", Workspace: "alpha"}

	var out bytes.Buffer
	if err := runRegister(t.Context(), clientFor(t, s), cfg, &out, nil); err != nil {
		t.Fatalf("runRegister: %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != "orchestrator" {
		t.Errorf("wrote %q, want %q from $BRO_IDENTITY", got, "orchestrator")
	}
	if _, err := s.Mailbox(t.Context(), "alpha", "orchestrator"); err != nil {
		t.Fatalf("Mailbox: %v", err)
	}
}

func TestRunRegisterGeneratesANameWhenNothingSuppliesOne(t *testing.T) {
	s := seeded(t)
	cfg := Config{Workspace: "alpha"}

	var out bytes.Buffer
	if err := runRegister(t.Context(), clientFor(t, s), cfg, &out, nil); err != nil {
		t.Fatalf("runRegister: %v", err)
	}

	name := strings.TrimSpace(out.String())
	if name == "" {
		t.Fatal("wrote nothing, want a generated name")
	}
	// Whatever it printed must be the name that was actually registered.
	if _, err := s.Mailbox(t.Context(), "alpha", name); err != nil {
		t.Fatalf("Mailbox(%q): %v", name, err)
	}
}

func TestRunRegisterGeneratesDistinctNames(t *testing.T) {
	s := seeded(t)
	cfg := Config{Workspace: "alpha"}

	seen := map[string]bool{}
	for i := range 20 {
		var out bytes.Buffer
		if err := runRegister(t.Context(), clientFor(t, s), cfg, &out, nil); err != nil {
			t.Fatalf("runRegister #%d: %v", i, err)
		}
		name := strings.TrimSpace(out.String())
		if seen[name] {
			t.Fatalf("generated %q twice in %d registrations", name, i+1)
		}
		seen[name] = true
	}
}

func TestRunRegisterRejectsADuplicate(t *testing.T) {
	s := seeded(t)
	cfg := Config{Workspace: "alpha"}

	var first bytes.Buffer
	if err := runRegister(t.Context(), clientFor(t, s), cfg, &first, []string{"-n", "worker-1"}); err != nil {
		t.Fatalf("first runRegister: %v", err)
	}

	var second bytes.Buffer
	err := runRegister(t.Context(), clientFor(t, s), cfg, &second, []string{"-n", "worker-1"})
	if !errors.Is(err, store.ErrMailboxExists) {
		t.Fatalf("second runRegister error = %v, want ErrMailboxExists", err)
	}
	if second.Len() != 0 {
		t.Errorf("wrote %q on the error path, want nothing", second.String())
	}
}

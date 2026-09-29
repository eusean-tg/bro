package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

// reader registers a mailbox named "reader" in alpha and returns the config
// that pulls as it.
func reader(t *testing.T, s *store.Store) Config {
	t.Helper()
	cfg := Config{Identity: "reader", Workspace: "alpha"}
	if _, err := s.Register(t.Context(), cfg.Workspace, cfg.Identity, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return cfg
}

func TestRunPullPeekCanBeRepeated(t *testing.T) {
	s := seeded(t)
	cfg := reader(t, s)
	if _, err := s.Register(t.Context(), "alpha", "writer", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := s.Tell(t.Context(), "alpha", "writer", "auth done", nil, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	var first, second bytes.Buffer
	if err := runPull(t.Context(), clientFor(t, s), cfg, &first, []string{"--peek"}); err != nil {
		t.Fatalf("first runPull: %v", err)
	}
	if err := runPull(t.Context(), clientFor(t, s), cfg, &second, []string{"--peek"}); err != nil {
		t.Fatalf("second runPull: %v", err)
	}

	if !strings.Contains(first.String(), "auth done") {
		t.Fatalf("first peek missing the message\ngot:\n%s", first.String())
	}
	if first.String() != second.String() {
		t.Errorf("peeks differ\nfirst:\n%s\nsecond:\n%s", first.String(), second.String())
	}
}

func TestRunPullSinceReadsPastTheCursor(t *testing.T) {
	s := seeded(t)
	cfg := reader(t, s)
	if _, err := s.Register(t.Context(), "alpha", "writer", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for _, body := range []string{"one", "two"} {
		if _, err := s.Tell(t.Context(), "alpha", "writer", body, nil, store.KindNote); err != nil {
			t.Fatalf("Tell: %v", err)
		}
	}

	// Consume everything.
	var drain bytes.Buffer
	if err := runPull(t.Context(), clientFor(t, s), cfg, &drain, nil); err != nil {
		t.Fatalf("runPull: %v", err)
	}

	var out bytes.Buffer
	if err := runPull(t.Context(), clientFor(t, s), cfg, &out, []string{"--since", "0"}); err != nil {
		t.Fatalf("runPull(--since 0): %v", err)
	}
	for _, want := range []string{"one", "two"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("--since 0 output is missing %q\ngot:\n%s", want, out.String())
		}
	}
}

func TestRunPullLastLimitsTheCount(t *testing.T) {
	s := seeded(t)
	cfg := reader(t, s)
	if _, err := s.Register(t.Context(), "alpha", "writer", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for _, body := range []string{"one", "two", "three"} {
		if _, err := s.Tell(t.Context(), "alpha", "writer", body, nil, store.KindNote); err != nil {
			t.Fatalf("Tell: %v", err)
		}
	}

	var out bytes.Buffer
	if err := runPull(t.Context(), clientFor(t, s), cfg, &out, []string{"--last", "1"}); err != nil {
		t.Fatalf("runPull(--last 1): %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "three") {
		t.Errorf("--last 1 should show the newest message\ngot:\n%s", got)
	}
	if strings.Contains(got, "one") || strings.Contains(got, "two") {
		t.Errorf("--last 1 showed more than one message\ngot:\n%s", got)
	}
}

func TestRunPullRejectsSinceAndLastTogether(t *testing.T) {
	s := seeded(t)
	cfg := reader(t, s)

	var out bytes.Buffer
	if err := runPull(t.Context(), clientFor(t, s), cfg, &out, []string{"--since", "0", "--last", "2"}); err == nil {
		t.Fatal("runPull accepted --since with --last, want an error")
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q on the error path, want nothing", out.String())
	}
}

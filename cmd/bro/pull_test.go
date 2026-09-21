package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunPullWritesTheMessagesItReturns(t *testing.T) {
	s := seeded(t,
		[3]string{"alpha", "worker-2", "storage schema landed"},
	)
	cfg := Config{Identity: "reader", Workspace: "alpha"}
	if _, err := s.Register(t.Context(), cfg.Workspace, cfg.Identity, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Registered after the message above, so nothing is waiting yet.
	var first bytes.Buffer
	if err := runPull(t.Context(), s, cfg, &first, nil); err != nil {
		t.Fatalf("runPull: %v", err)
	}
	if first.Len() != 0 {
		t.Errorf("wrote %q, want nothing before any new traffic", first.String())
	}

	if _, err := s.Tell(t.Context(), "alpha", "worker-2", "auth done"); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	var second bytes.Buffer
	if err := runPull(t.Context(), s, cfg, &second, nil); err != nil {
		t.Fatalf("runPull: %v", err)
	}
	if got := second.String(); !strings.Contains(got, "auth done") {
		t.Errorf("output is missing the new message\ngot:\n%s", got)
	}
}

func TestRunPullRequiresAnIdentity(t *testing.T) {
	s := seeded(t)
	cfg := Config{Workspace: "alpha"}

	var out bytes.Buffer
	if err := runPull(t.Context(), s, cfg, &out, nil); err == nil {
		t.Fatal("runPull succeeded without an identity, want an error")
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q on the error path, want nothing", out.String())
	}
}

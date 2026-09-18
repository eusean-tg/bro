package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

func TestRunTellWritesIDAndStoresMessage(t *testing.T) {
	s := seeded(t)
	cfg := Config{Identity: "worker-1", Workspace: "alpha"}

	var out bytes.Buffer
	if err := runTell(t.Context(), s, cfg, &out, []string{"started on auth"}); err != nil {
		t.Fatalf("runTell: %v", err)
	}

	// The printed id must be a number, and it must be the id the message got.
	printed := strings.TrimSpace(out.String())
	id, err := strconv.ParseInt(printed, 10, 64)
	if err != nil {
		t.Fatalf("runTell wrote %q, want a message id", out.String())
	}

	msgs, err := s.Log(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("stored %d messages, want 1", len(msgs))
	}

	got := msgs[0]
	if got.ID != id {
		t.Errorf("runTell printed id %d, message has id %d", id, got.ID)
	}
	if got.Sender != cfg.Identity {
		t.Errorf("Sender = %q, want %q", got.Sender, cfg.Identity)
	}
	if got.Body != "started on auth" {
		t.Errorf("Body = %q, want %q", got.Body, "started on auth")
	}
}

func TestRunTellWritesToTheGivenWorkspace(t *testing.T) {
	s := seeded(t)

	var out bytes.Buffer
	cfg := Config{Identity: "worker-1", Workspace: "alpha"}
	if err := runTell(t.Context(), s, cfg, &out, []string{"for alpha only"}); err != nil {
		t.Fatalf("runTell: %v", err)
	}

	other, err := s.Log(t.Context(), "beta")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("beta holds %d messages, want 0", len(other))
	}
}

func TestRunTellRejectsBadInput(t *testing.T) {
	// Each case must fail before anything is written or stored. The wording
	// of each error is deliberately not asserted.
	tests := []struct {
		name     string
		identity string
		rest     []string
	}{
		{name: "no message", identity: "worker-1", rest: nil},
		{name: "more than one positional", identity: "worker-1", rest: []string{"hi", "--kind", "control"}},
		{name: "no identity", identity: "", rest: []string{"hi"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := seeded(t)
			cfg := Config{Identity: tt.identity, Workspace: "alpha"}

			var out bytes.Buffer
			if err := runTell(t.Context(), s, cfg, &out, tt.rest); err == nil {
				t.Fatal("runTell succeeded, want an error")
			}

			if out.Len() != 0 {
				t.Errorf("wrote %q on the error path, want nothing", out.String())
			}
			msgs, err := s.Log(t.Context(), "alpha")
			if err != nil {
				t.Fatalf("Log: %v", err)
			}
			if len(msgs) != 0 {
				t.Errorf("stored %d messages on the error path, want 0", len(msgs))
			}
		})
	}
}

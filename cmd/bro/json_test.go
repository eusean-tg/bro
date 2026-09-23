package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

// pullJSON runs pull with --json and decodes the envelope.
func pullJSON(t *testing.T, s *store.Store, cfg Config, args ...string) map[string]any {
	t.Helper()

	var out bytes.Buffer
	if err := runPull(t.Context(), clientFor(t, s), cfg, &out, append([]string{"--json"}, args...)); err != nil {
		t.Fatalf("runPull(--json): %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\ngot:\n%s", err, out.String())
	}
	return got
}

func TestRunPullJSONCarriesTheEnvelopeFields(t *testing.T) {
	s := seeded(t)
	ctx := t.Context()
	for _, name := range []string{"sender", "reader"} {
		if _, err := s.Register(ctx, "alpha", name, ""); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}
	if _, err := s.Tell(ctx, "alpha", "sender", "auth done", []string{"reader"}); err != nil {
		t.Fatalf("Tell: %v", err)
	}
	cfg := Config{Identity: "reader", Workspace: "alpha"}

	got := pullJSON(t, s, cfg, "--peek")

	for _, key := range []string{"messages", "latest_id", "unread_count"} {
		if _, ok := got[key]; !ok {
			t.Errorf("envelope has no %q field; got keys %v", key, keysOf(got))
		}
	}
	if n, _ := got["unread_count"].(float64); n != 1 {
		t.Errorf("unread_count = %v, want 1", got["unread_count"])
	}
}

func TestRunPullJSONUsesSnakeCaseMessageFields(t *testing.T) {
	s := seeded(t)
	ctx := t.Context()
	for _, name := range []string{"sender", "reader"} {
		if _, err := s.Register(ctx, "alpha", name, ""); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}
	if _, err := s.Tell(ctx, "alpha", "sender", "auth done", []string{"reader"}); err != nil {
		t.Fatalf("Tell: %v", err)
	}
	cfg := Config{Identity: "reader", Workspace: "alpha"}

	got := pullJSON(t, s, cfg)

	msgs, ok := got["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages = %v, want one message", got["messages"])
	}
	m, _ := msgs[0].(map[string]any)

	for _, key := range []string{"id", "sender", "kind", "body", "created_at", "mentioned"} {
		if _, ok := m[key]; !ok {
			t.Errorf("message has no %q field; got keys %v", key, keysOf(m))
		}
	}
	if m["body"] != "auth done" {
		t.Errorf("body = %v, want %q", m["body"], "auth done")
	}
	if m["mentioned"] != true {
		t.Errorf("mentioned = %v, want true", m["mentioned"])
	}
}

func TestRunPullJSONEncodesAnEmptyResultAsAnArray(t *testing.T) {
	s := seeded(t)
	cfg := Config{Identity: "reader", Workspace: "alpha"}
	if _, err := s.Register(t.Context(), "alpha", "reader", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	var out bytes.Buffer
	if err := runPull(t.Context(), clientFor(t, s), cfg, &out, []string{"--json"}); err != nil {
		t.Fatalf("runPull: %v", err)
	}

	// null would change the type a client sees when nobody has spoken.
	if strings.Contains(out.String(), `"messages":null`) {
		t.Errorf("empty result encoded messages as null\ngot:\n%s", out.String())
	}
	got := pullJSON(t, s, cfg)
	if msgs, ok := got["messages"].([]any); !ok || len(msgs) != 0 {
		t.Errorf("messages = %v, want an empty array", got["messages"])
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

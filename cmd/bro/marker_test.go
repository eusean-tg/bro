package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

// lineContaining returns the rendered line holding body, or fails.
func lineContaining(t *testing.T, out, body string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, body) {
			return line
		}
	}
	t.Fatalf("no line contains %q\ngot:\n%s", body, out)
	return ""
}

func TestRunPullMarksTheLinesThatNameTheReader(t *testing.T) {
	s := seeded(t)
	ctx := t.Context()
	for _, name := range []string{"sender", "reader", "other"} {
		if _, err := s.Register(ctx, "alpha", name, ""); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}

	for _, m := range []struct {
		body string
		to   []string
	}{
		{"addressed to you", []string{"reader"}},
		{"ambient chatter", nil},
		{"addressed to someone else", []string{"other"}},
	} {
		if _, err := s.Tell(ctx, "alpha", "sender", m.body, m.to, store.KindNote); err != nil {
			t.Fatalf("Tell(%q): %v", m.body, err)
		}
	}

	var out bytes.Buffer
	cfg := Config{Identity: "reader", Workspace: "alpha"}
	if err := runPull(ctx, clientFor(t, s), cfg, nil, &out, nil); err != nil {
		t.Fatalf("runPull: %v", err)
	}
	got := out.String()

	if line := lineContaining(t, got, "addressed to you"); !strings.HasPrefix(line, "@") {
		t.Errorf("line %q should start with the mention marker", line)
	}
	for _, body := range []string{"ambient chatter", "addressed to someone else"} {
		if line := lineContaining(t, got, body); strings.HasPrefix(line, "@") {
			t.Errorf("line %q carries a mention marker but does not name this reader", line)
		}
	}
}

func TestRunLogNeverMarksMentions(t *testing.T) {
	s := seeded(t)
	ctx := t.Context()
	for _, name := range []string{"sender", "reader"} {
		if _, err := s.Register(ctx, "alpha", name, ""); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}
	if _, err := s.Tell(ctx, "alpha", "sender", "addressed to reader", []string{"reader"}, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	var out bytes.Buffer
	if err := runLog(ctx, clientFor(t, s), Config{Workspace: "alpha"}, &out, nil); err != nil {
		t.Fatalf("runLog: %v", err)
	}

	// A transcript has no reader, so nothing in it is addressed to "you".
	if line := lineContaining(t, out.String(), "addressed to reader"); strings.HasPrefix(line, "@") {
		t.Errorf("log line %q carries a mention marker", line)
	}
}

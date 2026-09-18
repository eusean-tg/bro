package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

// seeded opens a store on a fresh database and writes the given messages,
// returning the store ready to read back.
func seeded(t *testing.T, msgs ...[3]string) *store.Store {
	t.Helper()

	s, err := store.Open(filepath.Join(t.TempDir(), "bro.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	for _, m := range msgs { // {workspace, sender, body}
		if _, err := s.Tell(t.Context(), m[0], m[1], m[2]); err != nil {
			t.Fatalf("Tell(%v): %v", m, err)
		}
	}
	return s
}

func TestRunLogWritesEveryMessageInOrder(t *testing.T) {
	s := seeded(t,
		[3]string{"alpha", "worker-1", "started on auth"},
		[3]string{"alpha", "worker-2", "started on storage"},
		[3]string{"alpha", "worker-1", "auth done"},
	)

	var out bytes.Buffer
	if err := runLog(t.Context(), s, Config{Workspace: "alpha"}, &out); err != nil {
		t.Fatalf("runLog: %v", err)
	}
	got := out.String()

	// The layout is yours; every message must be present, in order, and
	// attributed to its sender.
	want := []string{"started on auth", "started on storage", "auth done"}
	at := make([]int, len(want))
	for i, w := range want {
		at[i] = strings.Index(got, w)
		if at[i] < 0 {
			t.Errorf("output is missing %q\ngot:\n%s", w, got)
		}
		if i > 0 && at[i] < at[i-1] {
			t.Errorf("%q appears before %q; messages must be in id order\ngot:\n%s",
				w, want[i-1], got)
		}
	}
	for _, sender := range []string{"worker-1", "worker-2"} {
		if !strings.Contains(got, sender) {
			t.Errorf("output does not attribute %q\ngot:\n%s", sender, got)
		}
	}
}

func TestRunLogIsScopedToOneWorkspace(t *testing.T) {
	s := seeded(t,
		[3]string{"alpha", "worker-1", "belongs to alpha"},
		[3]string{"beta", "worker-2", "belongs to beta"},
	)

	var out bytes.Buffer
	if err := runLog(t.Context(), s, Config{Workspace: "alpha"}, &out); err != nil {
		t.Fatalf("runLog: %v", err)
	}

	if got := out.String(); !strings.Contains(got, "belongs to alpha") {
		t.Errorf("alpha's message is missing\ngot:\n%s", got)
	} else if strings.Contains(got, "belongs to beta") {
		t.Errorf("beta's message leaked into alpha's log\ngot:\n%s", got)
	}
}

func TestRunLogOnEmptyWorkspaceWritesNothing(t *testing.T) {
	s := seeded(t)

	var out bytes.Buffer
	if err := runLog(t.Context(), s, Config{Workspace: "never-used"}, &out); err != nil {
		t.Fatalf("runLog: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q, want nothing", out.String())
	}
}

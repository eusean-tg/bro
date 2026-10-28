package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/eusean-tg/bro/internal/store"
)

// waitRoom registers reader and writer in alpha and returns the config that
// pulls as reader.
func waitRoom(t *testing.T) (*store.Store, Config) {
	t.Helper()

	s := seeded(t)
	cfg := reader(t, s)
	if _, err := s.Register(t.Context(), "alpha", "writer", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return s, cfg
}

func TestRunPullWaitReturnsForAMention(t *testing.T) {
	s, cfg := waitRoom(t)
	if _, err := s.Tell(t.Context(), "alpha", "writer", "for you", []string{"reader"}, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	var out bytes.Buffer
	start := time.Now()
	if err := runPull(t.Context(), clientFor(t, s), cfg, nil, &out, []string{"--wait", "1m"}); err != nil {
		t.Fatalf("runPull --wait: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("runPull took %v with a mention already unread", d)
	}
	if !strings.Contains(out.String(), "for you") {
		t.Errorf("output missing the mention\ngot:\n%s", out.String())
	}
}

func TestRunPullWaitTimeoutIsErrTimedOutAndStillPrints(t *testing.T) {
	s, cfg := waitRoom(t)
	if _, err := s.Tell(t.Context(), "alpha", "writer", "to the room", nil, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	var out bytes.Buffer
	err := runPull(t.Context(), clientFor(t, s), cfg, nil, &out, []string{"--wait", "50ms"})
	if !errors.Is(err, errTimedOut) {
		t.Fatalf("err = %v, want errTimedOut", err)
	}
	if !strings.Contains(out.String(), "to the room") {
		t.Errorf("a timed-out wait must still print what arrived\ngot:\n%s", out.String())
	}
}

func TestRunPullWaitJSONCarriesTimedOut(t *testing.T) {
	s, cfg := waitRoom(t)

	var out bytes.Buffer
	err := runPull(t.Context(), clientFor(t, s), cfg, nil, &out, []string{"--json", "--wait", "50ms"})
	if !errors.Is(err, errTimedOut) {
		t.Fatalf("err = %v, want errTimedOut: --json changes the output, not the exit code", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\ngot:\n%s", err, out.String())
	}
	if got["timed_out"] != true {
		t.Errorf("timed_out = %v, want true\ngot:\n%s", got["timed_out"], out.String())
	}
}

func TestRunPullJSONWithoutWaitIsNotTimedOut(t *testing.T) {
	s, cfg := waitRoom(t)

	got := pullJSON(t, s, cfg)
	if got["timed_out"] != false {
		t.Errorf("timed_out = %v, want false for a pull that never waited", got["timed_out"])
	}
}

func TestRunPullWaitRejectsSinceAndLast(t *testing.T) {
	s, cfg := waitRoom(t)

	for _, args := range [][]string{
		{"--wait", "1m", "--since", "0"},
		{"--wait", "1m", "--last", "3"},
	} {
		var out bytes.Buffer
		start := time.Now()
		err := runPull(t.Context(), clientFor(t, s), cfg, nil, &out, args)
		if err == nil {
			t.Errorf("runPull %v succeeded, want an error: the wake check is measured from the cursor", args)
		}
		if errors.Is(err, errTimedOut) || time.Since(start) > 5*time.Second {
			t.Errorf("runPull %v waited before rejecting; flags must be checked before any request", args)
		}
	}
}

func TestRunPullWaitWorksWithPeek(t *testing.T) {
	s, cfg := waitRoom(t)
	if _, err := s.Tell(t.Context(), "alpha", "writer", "for you", []string{"reader"}, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	var peeked, plain bytes.Buffer
	if err := runPull(t.Context(), clientFor(t, s), cfg, nil, &peeked, []string{"--wait", "1m", "--peek"}); err != nil {
		t.Fatalf("runPull --wait --peek: %v", err)
	}
	if err := runPull(t.Context(), clientFor(t, s), cfg, nil, &plain, nil); err != nil {
		t.Fatalf("runPull: %v", err)
	}
	if !strings.Contains(plain.String(), "for you") {
		t.Errorf("the mention was consumed by a peeking wait\nplain pull got:\n%s", plain.String())
	}
}

func TestExitCode(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{nil, 0},
		{flag.ErrHelp, 0},
		{errors.New("boom"), 1},
		{errTimedOut, 3},
		{fmt.Errorf("pull: %w", errTimedOut), 3},
	} {
		if got := exitCode(tc.err); got != tc.want {
			t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/eusean-tg/bro/internal/store"
)

// closedRoom registers reader and writer in alpha and has writer close it.
func closedRoom(t *testing.T) (*store.Store, Config) {
	t.Helper()

	s, cfg := waitRoom(t)
	if _, err := s.CloseWorkspace(t.Context(), "alpha", "writer", "unit complete"); err != nil {
		t.Fatalf("CloseWorkspace: %v", err)
	}
	return s, cfg
}

func TestRunCloseWritesTheIDAndClosesTheUnit(t *testing.T) {
	s, _ := waitRoom(t)
	closer := Config{Identity: "writer", Workspace: "alpha"}

	var out bytes.Buffer
	if err := runClose(t.Context(), clientFor(t, s), closer, &out, nil); err != nil {
		t.Fatalf("runClose: %v", err)
	}
	id, err := strconv.ParseInt(strings.TrimSpace(out.String()), 10, 64)
	if err != nil {
		t.Fatalf("runClose wrote %q, want the close message's id", out.String())
	}

	msgs, err := s.Log(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != id || msgs[0].Kind != "control" || msgs[0].Body != "unit complete" {
		t.Errorf("stored %+v, want control message %d saying 'unit complete'", msgs, id)
	}
}

func TestRunCloseTakesAMessage(t *testing.T) {
	s, _ := waitRoom(t)
	closer := Config{Identity: "writer", Workspace: "alpha"}

	var out bytes.Buffer
	if err := runClose(t.Context(), clientFor(t, s), closer, &out, []string{"auth refactor merged"}); err != nil {
		t.Fatalf("runClose: %v", err)
	}
	msgs, _ := s.Log(t.Context(), "alpha")
	if len(msgs) != 1 || msgs[0].Body != "auth refactor merged" {
		t.Errorf("stored %+v, want the given message", msgs)
	}
}

func TestRunCloseRequiresAnIdentity(t *testing.T) {
	s, _ := waitRoom(t)

	var out bytes.Buffer
	if err := runClose(t.Context(), clientFor(t, s), Config{Workspace: "alpha"}, &out, nil); err == nil {
		t.Error("runClose succeeded with no identity")
	}
	if msgs, _ := s.Log(t.Context(), "alpha"); len(msgs) != 0 {
		t.Errorf("a rejected close stored %d messages", len(msgs))
	}
}

func TestRunPullOnAClosedUnitIsErrClosedAndStillPrints(t *testing.T) {
	s, cfg := closedRoom(t)

	var out bytes.Buffer
	err := runPull(t.Context(), clientFor(t, s), cfg, &out, nil)
	if !errors.Is(err, errClosed) {
		t.Fatalf("err = %v, want errClosed", err)
	}
	if !strings.Contains(out.String(), "unit complete") {
		t.Errorf("the close message was not printed\ngot:\n%s", out.String())
	}
}

func TestRunPullJSONCarriesClosed(t *testing.T) {
	s, cfg := closedRoom(t)

	var out bytes.Buffer
	err := runPull(t.Context(), clientFor(t, s), cfg, &out, []string{"--json"})
	if !errors.Is(err, errClosed) {
		t.Fatalf("err = %v, want errClosed", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\ngot:\n%s", err, out.String())
	}
	if got["closed"] != true {
		t.Errorf("closed = %v, want true", got["closed"])
	}
}

func TestRunPullWaitOnAClosedUnitReturnsAtOnceAsClosed(t *testing.T) {
	s, cfg := closedRoom(t)
	c := clientFor(t, s)

	var first bytes.Buffer
	_ = runPull(t.Context(), c, cfg, &first, nil) // reads the close

	var out bytes.Buffer
	start := time.Now()
	err := runPull(t.Context(), c, cfg, &out, []string{"--wait", "1m"})
	if !errors.Is(err, errClosed) {
		t.Errorf("err = %v, want errClosed: closed wins over waiting and over timing out", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("a closed worker's wait lasted %v; it should return at once", d)
	}
}

func TestExitCodeForClosed(t *testing.T) {
	for _, err := range []error{errClosed, fmt.Errorf("pull: %w", errClosed)} {
		if got := exitCode(err); got != 4 {
			t.Errorf("exitCode(%v) = %d, want 4", err, got)
		}
	}
}

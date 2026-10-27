package server_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eusean-tg/bro/internal/server"
	"github.com/eusean-tg/bro/internal/store"
)

// daemon is a handler at version "v1" that records what the version check
// did: whether it called stop, and how many times it asked stale.
type daemon struct {
	st      *store.Store
	h       http.Handler
	stopped bool
	checks  int
}

// versioned returns a daemon at version "v1". stale is the answer its
// executable check gives: true means the binary on disk was rebuilt.
func versioned(t *testing.T, stale bool) *daemon {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "bro.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	d := &daemon{st: st}
	d.h = server.New(st, "v1",
		func() { d.stopped = true },
		func() bool { d.checks++; return stale },
	)
	return d
}

// send POSTs body to path with the given Bro-Version; "" sends no header.
func send(h http.Handler, path, body, version string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if version != "" {
		req.Header.Set("Bro-Version", version)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAMismatchAtAStaleDaemonIs412AndStopsIt(t *testing.T) {
	d := versioned(t, true)

	rec := send(d.h, "/log", `{"workspace":"alpha"}`, "v2")
	wantError(t, rec, http.StatusPreconditionFailed)
	if !d.stopped {
		t.Error("stop was not called; a daemon whose binary was rebuilt must retire")
	}
}

func TestA412ClosesTheConnection(t *testing.T) {
	d := versioned(t, true)

	// The daemon is about to exit, so the client must not reuse this
	// connection for its retry.
	rec := send(d.h, "/log", `{"workspace":"alpha"}`, "v2")
	if got := rec.Header().Get("Connection"); got != "close" {
		t.Errorf("Connection header = %q, want close", got)
	}
}

func TestAMismatchAtAStaleDaemonNeverReachesTheHandler(t *testing.T) {
	d := versioned(t, true)

	send(d.h, "/register", `{"workspace":"alpha","name":"worker"}`, "v2")

	if _, err := d.st.Mailbox(t.Context(), "alpha", "worker"); err == nil {
		t.Error("the mailbox was registered by a request the stale daemon refused")
	}
}

func TestAMismatchAtACurrentDaemonIsServed(t *testing.T) {
	d := versioned(t, false)

	// The client is the stale side: a process started before the rebuild,
	// such as a worker still waiting. Retiring here would end the daemon
	// that the rebuild's own clients spawned.
	rec := send(d.h, "/register", `{"workspace":"alpha","name":"worker"}`, "v0")
	wantStatus(t, rec, http.StatusOK)
	if d.stopped {
		t.Error("stop was called; an older client must not retire the current daemon")
	}
	if _, err := d.st.Mailbox(t.Context(), "alpha", "worker"); err != nil {
		t.Errorf("the older client's register did not reach the store: %v", err)
	}
}

func TestAMatchingVersionIsServedWithoutACheck(t *testing.T) {
	d := versioned(t, true)

	rec := send(d.h, "/log", `{"workspace":"alpha"}`, "v1")
	wantStatus(t, rec, http.StatusOK)
	if d.stopped {
		t.Error("stop was called for a matching version")
	}
	if d.checks != 0 {
		t.Errorf("stale was called %d times for a matching version; it re-reads the whole binary", d.checks)
	}
}

func TestARequestWithoutAVersionIsServedWithoutACheck(t *testing.T) {
	d := versioned(t, true)

	// curl --unix-socket sends no Bro-Version; debugging must not retire
	// the daemon.
	rec := send(d.h, "/log", `{"workspace":"alpha"}`, "")
	wantStatus(t, rec, http.StatusOK)
	if d.stopped {
		t.Error("stop was called for a request with no version header")
	}
	if d.checks != 0 {
		t.Errorf("stale was called %d times for a request with no version header", d.checks)
	}
}

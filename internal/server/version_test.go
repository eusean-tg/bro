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

// versioned returns a handler at version "v1" whose stop func records
// whether it was called.
func versioned(t *testing.T) (*store.Store, http.Handler, *bool) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "bro.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	stopped := new(bool)
	return st, server.New(st, "v1", func() { *stopped = true }), stopped
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

func TestAMismatchedVersionIs412AndStopsTheDaemon(t *testing.T) {
	_, h, stopped := versioned(t)

	rec := send(h, "/log", `{"workspace":"alpha"}`, "v2")
	wantError(t, rec, http.StatusPreconditionFailed)
	if !*stopped {
		t.Error("stop was not called; a daemon that sees a newer client must retire")
	}
}

func TestA412ClosesTheConnection(t *testing.T) {
	_, h, _ := versioned(t)

	// The daemon is about to exit, so the client must not reuse this
	// connection for its retry.
	rec := send(h, "/log", `{"workspace":"alpha"}`, "v2")
	if got := rec.Header().Get("Connection"); got != "close" {
		t.Errorf("Connection header = %q, want close", got)
	}
}

func TestAMismatchedRequestNeverReachesTheHandler(t *testing.T) {
	st, h, _ := versioned(t)

	send(h, "/register", `{"workspace":"alpha","name":"worker"}`, "v2")

	if _, err := st.Mailbox(t.Context(), "alpha", "worker"); err == nil {
		t.Error("the mailbox was registered by a request with the wrong version")
	}
}

func TestAMatchingVersionIsServed(t *testing.T) {
	_, h, stopped := versioned(t)

	rec := send(h, "/log", `{"workspace":"alpha"}`, "v1")
	wantStatus(t, rec, http.StatusOK)
	if *stopped {
		t.Error("stop was called for a matching version")
	}
}

func TestARequestWithoutAVersionIsServed(t *testing.T) {
	_, h, stopped := versioned(t)

	// curl --unix-socket sends no Bro-Version; debugging must not retire
	// the daemon.
	rec := send(h, "/log", `{"workspace":"alpha"}`, "")
	wantStatus(t, rec, http.StatusOK)
	if *stopped {
		t.Error("stop was called for a request with no version header")
	}
}

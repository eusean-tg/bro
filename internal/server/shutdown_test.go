package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

// postCancelled sends a request whose context is already cancelled, as every
// in-flight request's is once the daemon stops.
func postCancelled(h http.Handler, path, body string) *httptest.ResponseRecorder {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Bro-Version", testVersion)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A store call cut off by cancellation rolls back, so the daemon changed
// nothing and the client may safely retry: that is what 503 promises. A 500
// here would surface a daemon restart to the caller as a failed tell.
func TestARequestCancelledByShutdownIs503(t *testing.T) {
	st, h := newServer(t)
	room(t, st)

	for _, c := range []struct{ path, body string }{
		{"/tell", `{"workspace":"alpha","sender":"writer","body":"cut off","to":["reader"]}`},
		{"/pull", `{"workspace":"alpha","me":"reader"}`},
		{"/log", `{"workspace":"alpha"}`},
		{"/register", `{"workspace":"alpha","name":"late"}`},
		{"/close", `{"workspace":"alpha","sender":"writer","body":"done"}`},
	} {
		wantError(t, postCancelled(h, c.path, c.body), http.StatusServiceUnavailable)
	}

	msgs, err := st.Log(t.Context(), "alpha", store.LogOptions{})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("a cancelled tell stored %d messages; a 503 must mean nothing changed", len(msgs))
	}
}

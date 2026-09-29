package server_test

import (
	"net/http"
	"testing"
	"testing/synctest"
)

func kinds(t *testing.T, h http.Handler) []string {
	t.Helper()

	rec := post(t, h, "/log", `{"workspace":"alpha"}`)
	wantStatus(t, rec, http.StatusOK)
	var out []string
	for _, m := range decode[struct {
		Messages []struct {
			Kind string `json:"kind"`
		} `json:"messages"`
	}](t, rec).Messages {
		out = append(out, m.Kind)
	}
	return out
}

func TestTellCarriesTheKind(t *testing.T) {
	st, h := newServer(t)
	room(t, st)

	wantStatus(t, post(t, h, "/tell", `{"workspace":"alpha","sender":"writer","body":"b","kind":"control"}`), http.StatusOK)
	// Requests from before kinds existed carry none; they are notes.
	wantStatus(t, post(t, h, "/tell", `{"workspace":"alpha","sender":"writer","body":"b"}`), http.StatusOK)

	if got := kinds(t, h); len(got) != 2 || got[0] != "control" || got[1] != "note" {
		t.Errorf("kinds = %q, want [control note]", got)
	}
}

func TestTellWithAnUnknownKindIs400(t *testing.T) {
	st, h := newServer(t)
	room(t, st)

	wantError(t, post(t, h, "/tell", `{"workspace":"alpha","sender":"writer","body":"b","kind":"contorl"}`), http.StatusBadRequest)
	if got := kinds(t, h); len(got) != 0 {
		t.Errorf("a rejected tell stored %q", got)
	}
}

func TestAWaitIsWokenByAControlMessage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, h := newServer(t)
		room(t, st)

		done := pulling(t.Context(), h, waitBody("reader", tenMinutes, ""))
		answered(done)
		wantStatus(t, post(t, h, "/tell", `{"workspace":"alpha","sender":"writer","body":"stop","kind":"control"}`), http.StatusOK)

		if rec := answered(done); rec == nil {
			t.Error("a waiting pull was not woken by a control message that names nobody")
		}
	})
}

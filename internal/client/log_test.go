package client_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/eusean-tg/bro/internal/server"
	"github.com/eusean-tg/bro/internal/store"
)

func TestLogSendsItsOptionsAndWait(t *testing.T) {
	var rec recorder
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		var body server.LogRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.add(body)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[]}`))
	})

	if _, err := c.Log(t.Context(), "alpha", store.LogOptions{After: 7, Last: 3}, 90*time.Second); err != nil {
		t.Fatalf("Log: %v", err)
	}

	want := server.LogRequest{Workspace: "alpha", After: 7, Last: 3, WaitMS: 90000}
	if got := rec.all(); len(got) != 1 || got[0] != want {
		t.Errorf("server saw %+v, want one request %+v", got, want)
	}
}

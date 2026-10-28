package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

func TestMailboxesReturnsTheWorkspacesMailboxes(t *testing.T) {
	st, h := newServer(t)
	register(t, st, "alpha", "writer")
	register(t, st, "alpha", "orchestrator")
	register(t, st, "beta", "elsewhere")

	rec := post(t, h, "/mailboxes", `{"workspace":"alpha"}`)
	wantStatus(t, rec, http.StatusOK)

	got := decode[struct {
		Mailboxes []store.Mailbox `json:"mailboxes"`
	}](t, rec)
	if len(got.Mailboxes) != 2 ||
		got.Mailboxes[0].Name != "orchestrator" ||
		got.Mailboxes[1].Name != "writer" {
		t.Errorf("mailboxes = %+v, want alpha's orchestrator then writer", got.Mailboxes)
	}
}

func TestMailboxesOfAnEmptyWorkspaceIsAnEmptyArray(t *testing.T) {
	_, h := newServer(t)

	rec := post(t, h, "/mailboxes", `{"workspace":"alpha"}`)
	wantStatus(t, rec, http.StatusOK)

	if !strings.Contains(rec.Body.String(), `"mailboxes":[]`) {
		t.Errorf("body = %s, want mailboxes encoded as []", rec.Body.String())
	}
}

package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eusean-tg/bro/internal/server"
	"github.com/eusean-tg/bro/internal/store"
)

const testVersion = "test"

// newServer returns a store on a fresh database and the handler serving it.
func newServer(t *testing.T) (*store.Store, http.Handler) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "bro.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	return st, server.New(st, testVersion, func() {}, func() bool { return true })
}

// post sends body to path the way the client will: POST, JSON, with the
// version header. The recorder captures the response without any socket.
func post(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Bro-Version", testVersion)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// decode reads the response body into a T, failing the test on bad JSON.
func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %T: %v\nbody: %s", v, err, rec.Body.String())
	}
	return v
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()

	if rec.Code != want {
		t.Fatalf("status = %d, want %d\nbody: %s", rec.Code, want, rec.Body.String())
	}
}

// wantError asserts the status and that the body is {"error": "<non-empty>"}.
func wantError(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()

	wantStatus(t, rec, want)
	body := decode[struct {
		Error string `json:"error"`
	}](t, rec)
	if body.Error == "" {
		t.Errorf("error body has no message: %s", rec.Body.String())
	}
}

func register(t *testing.T, st *store.Store, ws, name string) {
	t.Helper()

	if _, err := st.Register(t.Context(), ws, name, ""); err != nil {
		t.Fatalf("Register(%s/%s): %v", ws, name, err)
	}
}

func TestTellStoresTheMessageAndReturnsItsID(t *testing.T) {
	st, h := newServer(t)
	register(t, st, "alpha", "sender")

	rec := post(t, h, "/tell", `{"workspace":"alpha","sender":"sender","body":"hello"}`)
	wantStatus(t, rec, http.StatusOK)

	got := decode[struct {
		ID int64 `json:"id"`
	}](t, rec)
	if got.ID <= 0 {
		t.Fatalf("id = %d, want a positive id", got.ID)
	}

	msgs, err := st.Log(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != got.ID || msgs[0].Body != "hello" {
		t.Errorf("stored %+v, want one message with id %d and body hello", msgs, got.ID)
	}
}

func TestTellCarriesRecipients(t *testing.T) {
	st, h := newServer(t)
	register(t, st, "alpha", "sender")
	register(t, st, "alpha", "reader")

	rec := post(t, h, "/tell", `{"workspace":"alpha","sender":"sender","body":"hi","to":["reader"]}`)
	wantStatus(t, rec, http.StatusOK)

	res, err := st.Pull(t.Context(), "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(res.Messages) != 1 || !res.Messages[0].Mentioned {
		t.Errorf("reader got %+v, want one message that mentions them", res.Messages)
	}
}

func TestTellFromAnUnregisteredSenderIs404(t *testing.T) {
	_, h := newServer(t)

	rec := post(t, h, "/tell", `{"workspace":"alpha","sender":"ghost","body":"boo"}`)
	wantError(t, rec, http.StatusNotFound)
}

func TestLogReturnsOnlyTheWorkspacesMessagesInOrder(t *testing.T) {
	st, h := newServer(t)
	register(t, st, "alpha", "a")
	register(t, st, "beta", "b")
	for _, m := range [][3]string{
		{"alpha", "a", "first"},
		{"beta", "b", "elsewhere"},
		{"alpha", "a", "second"},
	} {
		if _, err := st.Tell(t.Context(), m[0], m[1], m[2], nil, store.KindNote); err != nil {
			t.Fatalf("Tell: %v", err)
		}
	}

	rec := post(t, h, "/log", `{"workspace":"alpha"}`)
	wantStatus(t, rec, http.StatusOK)

	got := decode[struct {
		Messages []store.Message `json:"messages"`
	}](t, rec)
	if len(got.Messages) != 2 ||
		got.Messages[0].Body != "first" ||
		got.Messages[1].Body != "second" {
		t.Errorf("messages = %+v, want alpha's first then second", got.Messages)
	}
}

func TestLogOfAnEmptyWorkspaceIsAnEmptyArray(t *testing.T) {
	_, h := newServer(t)

	rec := post(t, h, "/log", `{"workspace":"alpha"}`)
	wantStatus(t, rec, http.StatusOK)

	if !strings.Contains(rec.Body.String(), `"messages":[]`) {
		t.Errorf("body = %s, want messages encoded as []", rec.Body.String())
	}
}

func TestRegisterReturnsTheMailbox(t *testing.T) {
	_, h := newServer(t)

	rec := post(t, h, "/register", `{"workspace":"alpha","name":"worker","description":"does work"}`)
	wantStatus(t, rec, http.StatusOK)

	got := decode[store.Mailbox](t, rec)
	if got.Name != "worker" || got.Description != "does work" {
		t.Errorf("mailbox = %+v, want worker / does work", got)
	}
}

func TestRegisterTwiceIs409(t *testing.T) {
	_, h := newServer(t)
	body := `{"workspace":"alpha","name":"worker"}`

	wantStatus(t, post(t, h, "/register", body), http.StatusOK)
	wantError(t, post(t, h, "/register", body), http.StatusConflict)
}

func TestPullReturnsTheEnvelopeAndAdvancesTheCursor(t *testing.T) {
	st, h := newServer(t)
	register(t, st, "alpha", "writer")
	register(t, st, "alpha", "reader")
	id, err := st.Tell(t.Context(), "alpha", "writer", "news", nil, store.KindNote)
	if err != nil {
		t.Fatalf("Tell: %v", err)
	}

	rec := post(t, h, "/pull", `{"workspace":"alpha","me":"reader"}`)
	wantStatus(t, rec, http.StatusOK)

	got := decode[store.PullResult](t, rec)
	if len(got.Messages) != 1 || got.Messages[0].Body != "news" {
		t.Errorf("messages = %+v, want the one news message", got.Messages)
	}
	if got.LatestID != id {
		t.Errorf("latest_id = %d, want %d", got.LatestID, id)
	}

	again := decode[store.PullResult](t, post(t, h, "/pull", `{"workspace":"alpha","me":"reader"}`))
	if len(again.Messages) != 0 {
		t.Errorf("second pull returned %d messages, want 0: the first pull advances the cursor", len(again.Messages))
	}
}

func TestPullPassesOptionsThrough(t *testing.T) {
	st, h := newServer(t)
	register(t, st, "alpha", "writer")
	register(t, st, "alpha", "reader")
	var ids []int64
	for _, body := range []string{"one", "two", "three"} {
		id, err := st.Tell(t.Context(), "alpha", "writer", body, nil, store.KindNote)
		if err != nil {
			t.Fatalf("Tell: %v", err)
		}
		ids = append(ids, id)
	}

	peek := decode[store.PullResult](t, post(t, h, "/pull", `{"workspace":"alpha","me":"reader","peek":true}`))
	if len(peek.Messages) != 3 || peek.UnreadCount != 3 {
		t.Errorf("peek = %d messages, unread %d; want 3 and 3", len(peek.Messages), peek.UnreadCount)
	}

	last := decode[store.PullResult](t, post(t, h, "/pull", `{"workspace":"alpha","me":"reader","last":1}`))
	if len(last.Messages) != 1 || last.Messages[0].Body != "three" {
		t.Errorf("last 1 = %+v, want only three", last.Messages)
	}

	since := decode[store.PullResult](t, post(t, h, "/pull",
		`{"workspace":"alpha","me":"reader","since":`+jsonInt(ids[1])+`}`))
	if len(since.Messages) != 1 || since.Messages[0].Body != "three" {
		t.Errorf("since %d = %+v, want only three", ids[1], since.Messages)
	}

	plain := decode[store.PullResult](t, post(t, h, "/pull", `{"workspace":"alpha","me":"reader"}`))
	if len(plain.Messages) != 3 {
		t.Errorf("plain pull after peek/last/since = %d messages, want 3: none of those advance the cursor", len(plain.Messages))
	}
}

func TestPullForAnUnknownMailboxIs404(t *testing.T) {
	_, h := newServer(t)

	rec := post(t, h, "/pull", `{"workspace":"alpha","me":"nobody"}`)
	wantError(t, rec, http.StatusNotFound)
}

func TestMalformedJSONIs400(t *testing.T) {
	_, h := newServer(t)

	for _, path := range []string{"/tell", "/log", "/register", "/pull", "/close"} {
		t.Run(path, func(t *testing.T) {
			wantError(t, post(t, h, path, `{"workspace":`), http.StatusBadRequest)
		})
	}
}

func TestRoutesOnlyAcceptPOST(t *testing.T) {
	_, h := newServer(t)

	req := httptest.NewRequest(http.MethodGet, "/log", nil)
	req.Header.Set("Bro-Version", testVersion)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /log status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func jsonInt(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

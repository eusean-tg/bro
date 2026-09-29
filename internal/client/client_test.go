package client_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eusean-tg/bro/internal/client"
	"github.com/eusean-tg/bro/internal/server"
	"github.com/eusean-tg/bro/internal/store"
)

const testVersion = "test"

// newClient returns a store on a fresh database and a client talking to a
// real server over it. The server listens on loopback TCP rather than a unix
// socket, so these tests exercise every method without touching a socket
// path; TestNewDialsTheUnixSocket covers the socket itself.
func newClient(t *testing.T) (*store.Store, *client.Client) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "bro.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	ts := httptest.NewServer(server.New(st, testVersion, func() {}))
	t.Cleanup(ts.Close)

	return st, &client.Client{BaseURL: ts.URL, HTTP: ts.Client(), Version: testVersion}
}

// clientFor returns a client whose every request is answered by h.
func clientFor(t *testing.T, h http.HandlerFunc) *client.Client {
	t.Helper()

	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	return &client.Client{BaseURL: ts.URL, HTTP: ts.Client(), Version: testVersion}
}

func register(t *testing.T, st *store.Store, ws, name string) {
	t.Helper()

	if _, err := st.Register(t.Context(), ws, name, ""); err != nil {
		t.Fatalf("Register(%s/%s): %v", ws, name, err)
	}
}

func TestTellReturnsTheStoredID(t *testing.T) {
	st, c := newClient(t)
	register(t, st, "alpha", "sender")
	register(t, st, "alpha", "reader")

	id, err := c.Tell(t.Context(), "alpha", "sender", "hello", []string{"reader"}, store.KindNote)
	if err != nil {
		t.Fatalf("Tell: %v", err)
	}

	res, err := st.Pull(t.Context(), "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(res.Messages) != 1 ||
		res.Messages[0].ID != id ||
		res.Messages[0].Body != "hello" ||
		!res.Messages[0].Mentioned {
		t.Errorf("stored %+v, want one message with id %d, body hello, mentioning reader", res.Messages, id)
	}
}

func TestTellToAnUnknownRecipientIsErrNoMailbox(t *testing.T) {
	st, c := newClient(t)
	register(t, st, "alpha", "sender")

	_, err := c.Tell(t.Context(), "alpha", "sender", "hi", []string{"ghost"}, store.KindNote)
	if !errors.Is(err, store.ErrNoMailbox) {
		t.Fatalf("err = %v, want store.ErrNoMailbox", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("err = %q, want the server's message naming ghost", err)
	}
}

func TestLogReturnsTheMessagesInOrder(t *testing.T) {
	st, c := newClient(t)
	register(t, st, "alpha", "a")
	for _, body := range []string{"first", "second"} {
		if _, err := st.Tell(t.Context(), "alpha", "a", body, nil, store.KindNote); err != nil {
			t.Fatalf("Tell: %v", err)
		}
	}

	msgs, err := c.Log(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Body != "first" || msgs[1].Body != "second" {
		t.Errorf("messages = %+v, want first then second", msgs)
	}
}

func TestLogOfAnEmptyWorkspaceIsEmpty(t *testing.T) {
	_, c := newClient(t)

	msgs, err := c.Log(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("messages = %+v, want none", msgs)
	}
}

func TestRegisterReturnsTheMailbox(t *testing.T) {
	_, c := newClient(t)

	mb, err := c.Register(t.Context(), "alpha", "worker", "does work")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if mb.Name != "worker" || mb.Description != "does work" {
		t.Errorf("mailbox = %+v, want worker / does work", mb)
	}
}

func TestRegisterTwiceIsErrMailboxExists(t *testing.T) {
	_, c := newClient(t)

	if _, err := c.Register(t.Context(), "alpha", "worker", ""); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	_, err := c.Register(t.Context(), "alpha", "worker", "")
	if !errors.Is(err, store.ErrMailboxExists) {
		t.Errorf("err = %v, want store.ErrMailboxExists", err)
	}
}

func TestPullReturnsTheResultAndAdvancesTheCursor(t *testing.T) {
	st, c := newClient(t)
	register(t, st, "alpha", "writer")
	register(t, st, "alpha", "reader")
	id, err := st.Tell(t.Context(), "alpha", "writer", "news", nil, store.KindNote)
	if err != nil {
		t.Fatalf("Tell: %v", err)
	}

	res, err := c.Pull(t.Context(), "alpha", "reader", store.PullOptions{}, 0)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Body != "news" || res.LatestID != id {
		t.Errorf("result = %+v, want the news message and latest_id %d", res, id)
	}

	again, err := c.Pull(t.Context(), "alpha", "reader", store.PullOptions{}, 0)
	if err != nil {
		t.Fatalf("second Pull: %v", err)
	}
	if len(again.Messages) != 0 {
		t.Errorf("second pull returned %d messages, want 0", len(again.Messages))
	}
}

func TestPullSendsTheOptions(t *testing.T) {
	st, c := newClient(t)
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

	since, err := c.Pull(t.Context(), "alpha", "reader", store.PullOptions{Since: &ids[1], Peek: true}, 0)
	if err != nil {
		t.Fatalf("Pull(since): %v", err)
	}
	if len(since.Messages) != 1 || since.Messages[0].Body != "three" {
		t.Errorf("since %d = %+v, want only three", ids[1], since.Messages)
	}

	last, err := c.Pull(t.Context(), "alpha", "reader", store.PullOptions{Last: 2}, 0)
	if err != nil {
		t.Fatalf("Pull(last): %v", err)
	}
	if len(last.Messages) != 2 || last.Messages[0].Body != "two" {
		t.Errorf("last 2 = %+v, want two and three", last.Messages)
	}

	peek, err := c.Pull(t.Context(), "alpha", "reader", store.PullOptions{Peek: true}, 0)
	if err != nil {
		t.Fatalf("Pull(peek): %v", err)
	}
	if len(peek.Messages) != 3 {
		t.Errorf("peek after since/last = %d messages, want 3: neither advances the cursor", len(peek.Messages))
	}
}

func TestPullOfAnUnknownMailboxIsErrNoMailbox(t *testing.T) {
	_, c := newClient(t)

	_, err := c.Pull(t.Context(), "alpha", "nobody", store.PullOptions{}, 0)
	if !errors.Is(err, store.ErrNoMailbox) {
		t.Errorf("err = %v, want store.ErrNoMailbox", err)
	}
}

func TestAnUnexpectedStatusCarriesTheServersMessage(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"disk on fire"}`))
	})

	_, err := c.Log(t.Context(), "alpha")
	if err == nil {
		t.Fatal("Log succeeded against a 500")
	}
	if !strings.Contains(err.Error(), "disk on fire") {
		t.Errorf("err = %q, want the server's message", err)
	}
	if errors.Is(err, store.ErrNoMailbox) || errors.Is(err, store.ErrMailboxExists) {
		t.Errorf("err = %v; a 500 is not a store sentinel", err)
	}
}

func TestEveryRequestIsAVersionedPOST(t *testing.T) {
	var method, path, version string
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		method, path, version = r.Method, r.URL.Path, r.Header.Get("Bro-Version")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[]}`))
	})
	c.Version = "v-under-test"

	if _, err := c.Log(t.Context(), "alpha"); err != nil {
		t.Fatalf("Log: %v", err)
	}
	if method != http.MethodPost || path != "/log" {
		t.Errorf("request = %s %s, want POST /log", method, path)
	}
	if version != "v-under-test" {
		t.Errorf("Bro-Version = %q, want v-under-test", version)
	}
}

func TestCallsHonorTheContext(t *testing.T) {
	_, c := newClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := c.Log(ctx, "alpha")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestNewDialsTheUnixSocket(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "bro.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	register(t, st, "alpha", "a")
	if _, err := st.Tell(t.Context(), "alpha", "a", "over the socket", nil, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	// Unix socket paths are limited to about 104 bytes on macOS, and
	// t.TempDir's path, which embeds the test name, can exceed that.
	dir, err := os.MkdirTemp("", "bro")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")

	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := &http.Server{Handler: server.New(st, testVersion, func() {})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { srv.Close() })

	c := client.New(sock, "", testVersion)
	msgs, err := c.Log(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Log over %s: %v", sock, err)
	}
	if len(msgs) != 1 || msgs[0].Body != "over the socket" {
		t.Errorf("messages = %+v, want the one message", msgs)
	}
}

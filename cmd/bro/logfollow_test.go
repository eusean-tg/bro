package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eusean-tg/bro/internal/client"
	"github.com/eusean-tg/bro/internal/server"
	"github.com/eusean-tg/bro/internal/store"
)

// syncBuffer is a bytes.Buffer a follow can write to while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// following runs runLog with args in alpha on its own goroutine. stop
// cancels its context, as Ctrl-C ends a terminal's follow, and returns what
// runLog returned.
func following(t *testing.T, c *client.Client, args ...string) (out *syncBuffer, stop func() error) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	out = &syncBuffer{}
	errc := make(chan error, 1)
	go func() { errc <- runLog(ctx, c, Config{Workspace: "alpha"}, out, args) }()

	return out, func() error {
		t.Helper()
		cancel()
		select {
		case err := <-errc:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("runLog --follow still running 5s after its context was cancelled")
			return nil
		}
	}
}

// eventually waits up to 5s for out to contain s.
func eventually(t *testing.T, out *syncBuffer, s string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), s) {
		if time.Now().After(deadline) {
			t.Fatalf("output never showed %q\ngot:\n%s", s, out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// post tells through the client, so the server publishes and a waiting
// follow wakes. A Tell on the store directly would wake nobody.
func post(t *testing.T, c *client.Client, body string) {
	t.Helper()

	if _, err := c.Tell(t.Context(), "alpha", "writer", body, nil, store.KindNote); err != nil {
		t.Fatalf("Tell(%q): %v", body, err)
	}
}

func TestRunLogLastPrintsOnlyTheNewest(t *testing.T) {
	s := seeded(t,
		[3]string{"alpha", "writer", "first"},
		[3]string{"alpha", "writer", "second"},
		[3]string{"alpha", "writer", "third"},
	)

	var out bytes.Buffer
	if err := runLog(t.Context(), clientFor(t, s), Config{Workspace: "alpha"}, &out, []string{"--last", "2"}); err != nil {
		t.Fatalf("runLog --last 2: %v", err)
	}
	if got := out.String(); strings.Contains(got, "first") || !strings.Contains(got, "second") || !strings.Contains(got, "third") {
		t.Errorf("--last 2 printed:\n%s\nwant second and third only", got)
	}
}

func TestRunLogRejectsANegativeLast(t *testing.T) {
	s := seeded(t)

	var out bytes.Buffer
	if err := runLog(t.Context(), clientFor(t, s), Config{Workspace: "alpha"}, &out, []string{"--last", "-1"}); err == nil {
		t.Error("runLog --last -1 = nil, want an error")
	}
}

func TestRunLogFollowPrintsEachNewMessageOnce(t *testing.T) {
	s := seeded(t, [3]string{"alpha", "writer", "already here"})
	c := clientFor(t, s)

	out, stop := following(t, c, "--follow")
	eventually(t, out, "already here")
	for _, body := range []string{"posted while following", "and another"} {
		post(t, c, body)
		eventually(t, out, body)
	}

	if err := stop(); err != nil {
		t.Errorf("runLog --follow = %v after its context was cancelled, want nil: cancelling is how a follow ends", err)
	}
	got := out.String()
	for _, body := range []string{"already here", "posted while following", "and another"} {
		if n := strings.Count(got, body); n != 1 {
			t.Errorf("%q printed %d times, want once\ngot:\n%s", body, n, got)
		}
	}
}

func TestRunLogFollowWithLastCarriesOnFromTheNewest(t *testing.T) {
	s := seeded(t,
		[3]string{"alpha", "writer", "old one"},
		[3]string{"alpha", "writer", "old two"},
		[3]string{"alpha", "writer", "newest"},
	)
	c := clientFor(t, s)

	out, stop := following(t, c, "--last", "1", "--follow")
	eventually(t, out, "newest")
	post(t, c, "fresh")
	eventually(t, out, "fresh")
	if err := stop(); err != nil {
		t.Errorf("runLog --follow = %v after its context was cancelled, want nil", err)
	}

	if got := out.String(); strings.Contains(got, "old one") || strings.Contains(got, "old two") {
		t.Errorf("--last 1 --follow printed messages older than the last one:\n%s", got)
	}
}

// requested waits up to 5s for n to reach want.
func requested(t *testing.T, n *atomic.Int64, want int64) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for n.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("%d log requests after 5s, want %d", n.Load(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunLogFollowOnAnEmptyWorkspacePrintsTheFirstMessage(t *testing.T) {
	s := seeded(t)
	if _, err := s.Register(t.Context(), "alpha", "writer", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, requests := countingClient(t, s)

	// The second request is the follow's wait, so the first read has come
	// back empty by the time anything is posted.
	out, stop := following(t, c, "--follow")
	requested(t, requests, 2)
	post(t, c, "first words")
	eventually(t, out, "first words")
	if err := stop(); err != nil {
		t.Errorf("runLog --follow = %v after its context was cancelled, want nil", err)
	}
}

// latestID returns the id of the newest message in alpha.
func latestID(t *testing.T, s *store.Store) string {
	t.Helper()

	msgs, err := s.Log(t.Context(), "alpha", store.LogOptions{Last: 1})
	if err != nil || len(msgs) != 1 {
		t.Fatalf("Log(alpha, Last 1) = %v, %v; want one message", msgs, err)
	}
	return strconv.FormatInt(msgs[0].ID, 10)
}

// --since N reads after message N, as pull --since does.
func TestRunLogSincePrintsOnlyLaterMessages(t *testing.T) {
	s := seeded(t, [3]string{"alpha", "writer", "first"})
	since := latestID(t, s)
	for _, body := range []string{"second", "third"} {
		if _, err := s.Tell(t.Context(), "alpha", "writer", body, nil, store.KindNote); err != nil {
			t.Fatalf("Tell: %v", err)
		}
	}

	var out bytes.Buffer
	if err := runLog(t.Context(), clientFor(t, s), Config{Workspace: "alpha"}, &out, []string{"--since", since}); err != nil {
		t.Fatalf("runLog --since %s: %v", since, err)
	}
	if got := out.String(); strings.Contains(got, "first") || !strings.Contains(got, "second") || !strings.Contains(got, "third") {
		t.Errorf("--since %s printed:\n%s\nwant second and third only", since, got)
	}
}

// A follow carries on from where the first read started when that read
// printed nothing, not from the start of the room.
func TestRunLogFollowFromSinceSkipsWhatCameBefore(t *testing.T) {
	s := seeded(t, [3]string{"alpha", "writer", "before"})
	since := latestID(t, s)
	c, requests := countingClient(t, s)

	out, stop := following(t, c, "--since", since, "--follow")
	requested(t, requests, 2)
	post(t, c, "after")
	eventually(t, out, "after")
	if err := stop(); err != nil {
		t.Errorf("runLog --follow = %v after its context was cancelled, want nil", err)
	}

	if got := out.String(); strings.Contains(got, "before") {
		t.Errorf("--since %s --follow printed a message from before %s:\n%s", since, since, got)
	}
}

// countingClient is clientFor with a count of the /log requests it sends.
func countingClient(t *testing.T, s *store.Store) (*client.Client, *atomic.Int64) {
	t.Helper()

	var n atomic.Int64
	h := server.New(s, "test", func() {}, func() bool { return true })
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/log" {
			n.Add(1)
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)

	return &client.Client{BaseURL: ts.URL, HTTP: ts.Client(), Version: "test"}, &n
}

// While nothing is posted, a follow sits in one request the daemon holds
// open. Asking again and again, with or without a pause, shows up here.
func TestRunLogFollowLetsTheDaemonDoTheWaiting(t *testing.T) {
	s := seeded(t, [3]string{"alpha", "writer", "already here"})
	c, requests := countingClient(t, s)

	out, stop := following(t, c, "--follow")
	eventually(t, out, "already here")
	time.Sleep(300 * time.Millisecond)
	n := requests.Load()
	_ = stop()

	if n > 2 {
		t.Errorf("follow sent %d log requests in 300ms with nothing new, want at most 2: the first read and one wait the daemon holds", n)
	}
}

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/eusean-tg/bro/internal/client"
	"github.com/eusean-tg/bro/internal/server"
	"github.com/eusean-tg/bro/internal/store"
)

// A daemon that stops while a worker waits must hand the wait on, not drop
// it: the wait is cancelled, the handler answers 503 without pulling, the
// client retries, the retry spawns a fresh daemon, and the wait resumes
// there with the cursor where it was.
func TestAWaitMovesToTheNextDaemon(t *testing.T) {
	_, sock, db := daemonPaths(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("BRO_DB", db)
	t.Setenv("BRO_TEST_DAEMON_VERSION", "v1")
	t.Cleanup(func() { retire(t, sock) })
	ctx := t.Context()

	c := client.New(sock, exe, "v1")
	for _, name := range []string{"reader", "writer"} {
		if _, err := c.Register(ctx, "alpha", name, ""); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}
	if _, err := c.Tell(ctx, "alpha", "writer", "said before the restart", nil, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	type result struct {
		res server.PullResponse
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := c.Pull(ctx, "alpha", "reader", store.PullOptions{}, 30*time.Second)
		done <- result{res, err}
	}()
	time.Sleep(300 * time.Millisecond) // let the pull reach the daemon and start waiting

	// Stop the daemon under the waiter. A request from a version no daemon
	// has retires it, and a client with no executable never respawns it.
	_, _ = client.New(sock, "", "stop").Log(ctx, "")

	select {
	case r := <-done:
		t.Fatalf("the pull returned when its daemon stopped (res %+v, err %v); it should have moved to the next daemon and kept waiting", r.res, r.err)
	case <-time.After(time.Second):
	}

	// If the old daemon is still draining its waiter, this tell cannot
	// start a new one: the old daemon holds the lock for Shutdown's full
	// timeout.
	if _, err := c.Tell(ctx, "alpha", "writer", "for you", []string{"reader"}, store.KindNote); err != nil {
		t.Fatalf("Tell after the restart: %v; did stopping the daemon cancel the waiting request (BaseContext)?", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Pull: %v", r.err)
		}
		if r.res.TimedOut {
			t.Error("TimedOut = true for a wait woken by a mention")
		}
		var got []string
		for _, m := range r.res.Messages {
			got = append(got, m.Body)
		}
		if len(got) != 2 || got[0] != "said before the restart" || got[1] != "for you" {
			t.Errorf("messages = %q, want both: the cancelled wait must not have moved the cursor", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the pull was not woken on the new daemon")
	}
}

// Stopping the daemon by signal must not wait out Shutdown's timeout on a
// waiter: the wait is cancelled at once and runDaemon returns promptly.
func TestStoppingTheDaemonDoesNotWaitForWaiters(t *testing.T) {
	_, sock, db := daemonPaths(t)
	stop, errc := startDaemon(t, sock, db)
	ctx := t.Context()

	c := client.New(sock, "", "test")
	if _, err := c.Register(ctx, "alpha", "reader", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	pulled := make(chan error, 1)
	go func() {
		_, err := c.Pull(context.Background(), "alpha", "reader", store.PullOptions{}, time.Minute)
		pulled <- err
	}()
	time.Sleep(300 * time.Millisecond)

	start := time.Now()
	stop()
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("runDaemon = %v, want nil: a clean stop, not a Shutdown timeout", err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("runDaemon took %v to return with a waiter; it should cancel the wait, not drain it", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runDaemon did not return after being stopped")
	}

	// The waiter's retries find no daemon and, with no executable, cannot
	// spawn one, so the pull fails rather than hanging.
	select {
	case <-pulled:
	case <-time.After(10 * time.Second):
		t.Error("the waiting pull never returned after its daemon stopped")
	}
}

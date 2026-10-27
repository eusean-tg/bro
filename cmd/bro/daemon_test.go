package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eusean-tg/bro/internal/client"
	"github.com/eusean-tg/bro/internal/store"
)

// daemonPaths returns a socket and database path in a fresh directory.
// Unix socket paths are limited to about 104 bytes on macOS, and
// t.TempDir's path, which embeds the test name, can exceed that.
func daemonPaths(t *testing.T) (dir, sock, db string) {
	t.Helper()

	dir, err := os.MkdirTemp("", "bro")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	return dir, filepath.Join(dir, "s.sock"), filepath.Join(dir, "data.db")
}

// testExe returns the path of the running test binary, which stands in
// for bro's own executable.
func testExe(t *testing.T) string {
	t.Helper()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return exe
}

// startDaemon runs runDaemon in the background at version "test" and
// waits until its socket accepts connections. The test binary never
// hashes to "test", so a mismatched request retires this daemon.
// Cancelling the returned func stops it; the channel receives runDaemon's
// return value.
func startDaemon(t *testing.T, sock, db string) (context.CancelFunc, <-chan error) {
	t.Helper()

	return startDaemonAt(t, sock, db, "test")
}

// startDaemonAt is startDaemon at the given version.
func startDaemonAt(t *testing.T, sock, db, version string) (context.CancelFunc, <-chan error) {
	t.Helper()

	exe := testExe(t)
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		errc <- runDaemon(ctx, sock, db, exe, version)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("daemon did not stop within 5s of cancellation")
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			conn.Close()
			return cancel, errc
		}
		select {
		case err := <-errc:
			t.Fatalf("runDaemon returned before listening: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon never accepted on %s: %v", sock, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitReturn waits for runDaemon to return, failing the test after d.
func waitReturn(t *testing.T, errc <-chan error, d time.Duration) error {
	t.Helper()

	select {
	case err := <-errc:
		return err
	case <-time.After(d):
		t.Fatalf("runDaemon still running after %v", d)
		return nil
	}
}

func TestDaemonServesOverTheSocket(t *testing.T) {
	_, sock, db := daemonPaths(t)
	startDaemon(t, sock, db)
	c := client.New(sock, "", "test")
	ctx := t.Context()

	if _, err := c.Register(ctx, "alpha", "worker", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := c.Tell(ctx, "alpha", "worker", "over the socket", nil, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}
	msgs, err := c.Log(ctx, "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Body != "over the socket" {
		t.Errorf("messages = %+v, want the one message", msgs)
	}
}

func TestDaemonStopsCleanlyWhenItsContextIsCancelled(t *testing.T) {
	_, sock, db := daemonPaths(t)
	cancel, errc := startDaemon(t, sock, db)

	cancel()
	if err := waitReturn(t, errc, 5*time.Second); err != nil {
		t.Errorf("runDaemon = %v, want nil after cancellation", err)
	}
	if _, err := os.Stat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket still present after shutdown (stat err = %v)", err)
	}
}

func TestDaemonLocksBesideTheSocket(t *testing.T) {
	dir, sock, db := daemonPaths(t)
	startDaemon(t, sock, db)

	if _, err := os.Stat(filepath.Join(dir, "daemon.lock")); err != nil {
		t.Errorf("daemon.lock beside the socket: %v", err)
	}
}

func TestASecondDaemonExitsQuietlyAndLeavesTheFirstServing(t *testing.T) {
	_, sock, db := daemonPaths(t)
	startDaemon(t, sock, db)

	// A second daemon on the same paths must see the lock and return nil
	// at once. If it serves instead, this context ends it after 5s and the
	// elapsed-time check fails.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := runDaemon(ctx, sock, db, testExe(t), "test"); err != nil {
		t.Errorf("second runDaemon = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("second runDaemon ran for %v; it must exit when the lock is held", elapsed)
	}

	c := client.New(sock, "", "test")
	if _, err := c.Log(t.Context(), "alpha"); err != nil {
		t.Errorf("first daemon unreachable after the second exited: %v", err)
	}
}

func TestDaemonReplacesAStaleSocketFile(t *testing.T) {
	_, sock, db := daemonPaths(t)

	// A daemon killed with SIGKILL leaves its socket file behind. Simulate
	// that: listen, then close without unlinking.
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("stale socket was not left behind: %v", err)
	}

	startDaemon(t, sock, db)

	c := client.New(sock, "", "test")
	if _, err := c.Log(t.Context(), "alpha"); err != nil {
		t.Errorf("Log after replacing a stale socket: %v", err)
	}
}

package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/eusean-tg/bro/internal/client"
	"github.com/eusean-tg/bro/internal/store"
)

// The handoff test needs real daemons in separate processes, built from
// different versions. The test binary plays `bro daemon`: spawned as
// `<test binary> daemon` with BRO_TEST_DAEMON_VERSION set, TestMain runs
// the real runDaemon at that version instead of the tests. The test
// binary never hashes to a made-up version, so each of these daemons
// finds its executable changed, as a rebuilt one would, and retires on a
// mismatch.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		if v := os.Getenv("BRO_TEST_DAEMON_VERSION"); v != "" {
			exe, err := os.Executable()
			if err != nil {
				os.Exit(1)
			}
			if err := runDaemon(context.Background(), os.Getenv("BRO_SOCK"), os.Getenv("BRO_DB"), exe, v); err != nil {
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

// retire stops whatever daemon serves sock by sending it a request from a
// version no daemon has, through a client that never spawns, then waits
// for the socket to disappear.
func retire(t *testing.T, sock string) {
	t.Helper()

	_, _ = client.New(sock, "", "retire").Log(context.Background(), "")
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(sock); errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("daemon at %s did not retire", sock)
}

func TestANewerClientReplacesAnOlderDaemon(t *testing.T) {
	_, sock, db := daemonPaths(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("BRO_DB", db)
	t.Cleanup(func() { retire(t, sock) })
	ctx := t.Context()

	// An old build spawns its daemon and writes through it.
	t.Setenv("BRO_TEST_DAEMON_VERSION", "old")
	old := client.New(sock, exe, "old")
	if _, err := old.Register(ctx, "alpha", "worker", ""); err != nil {
		t.Fatalf("Register via the old build: %v", err)
	}
	if _, err := old.Tell(ctx, "alpha", "worker", "written by old", nil, store.KindNote); err != nil {
		t.Fatalf("Tell via the old build: %v", err)
	}

	// The binary is rebuilt. The new client's first call meets the old
	// daemon, which retires; the retry spawns a daemon from the new build.
	t.Setenv("BRO_TEST_DAEMON_VERSION", "new")
	msgs, err := client.New(sock, exe, "new").Log(ctx, "alpha")
	if err != nil {
		t.Fatalf("Log via the new build: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Body != "written by old" {
		t.Errorf("messages = %+v, want the message the old build wrote", msgs)
	}

	// The daemon now serving is the new one: a client that cannot spawn
	// reaches it with the new version, and no 412 intervenes.
	if _, err := client.New(sock, "", "new").Log(ctx, "alpha"); err != nil {
		t.Errorf("the new daemon is not the one serving: %v", err)
	}
}

func TestTheHandoffSurvivesRepeatedRebuilds(t *testing.T) {
	_, sock, db := daemonPaths(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("BRO_DB", db)
	t.Cleanup(func() { retire(t, sock) })

	for _, v := range []string{"v1", "v2", "v3", "v4", "v5"} {
		t.Setenv("BRO_TEST_DAEMON_VERSION", v)
		if _, err := client.New(sock, exe, v).Log(t.Context(), "alpha"); err != nil {
			t.Fatalf("Log at %s: %v", v, err)
		}
	}
}

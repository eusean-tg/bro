package client_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/eusean-tg/bro/internal/client"
	"github.com/eusean-tg/bro/internal/server"
	"github.com/eusean-tg/bro/internal/store"
)

// The spawn tests need an executable that behaves like `bro daemon`. The
// test binary plays that part: spawned as `<test binary> daemon` with
// BRO_TEST_FAKE_DAEMON set, TestMain runs fakeDaemon instead of the tests.
// This is the helper-process pattern the os/exec package uses in its own
// tests.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		if mode := os.Getenv("BRO_TEST_FAKE_DAEMON"); mode != "" {
			os.Exit(fakeDaemon(mode))
		}
	}
	os.Exit(m.Run())
}

// fakeDaemon is a stand-in for runDaemon. Mode "serve" takes the lock,
// replaces any stale socket, and serves at $BRO_SOCK over a store at
// $BRO_DB until its directory is removed. Mode "die" exits without
// listening. Every invocation appends a line to $BRO_TEST_SPAWN_LOG.
func fakeDaemon(mode string) int {
	if log := os.Getenv("BRO_TEST_SPAWN_LOG"); log != "" {
		f, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			_, _ = f.WriteString(mode + "\n")
			_ = f.Close()
		}
	}
	if mode == "die" {
		return 0
	}

	sock := os.Getenv("BRO_SOCK")
	if sock == "" {
		return 2 // the client must tell the daemon where to listen
	}
	dir := filepath.Dir(sock)

	lk, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 1
	}
	defer lk.Close()
	if err := syscall.Flock(int(lk.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return 0 // another fake daemon is serving
	}

	st, err := store.Open(os.Getenv("BRO_DB"))
	if err != nil {
		return 1
	}
	defer st.Close()

	_ = os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		return 1
	}
	defer l.Close()
	go func() { _ = http.Serve(l, server.New(st, testVersion, func() {}, func() bool { return true })) }()

	// Detached, so nothing will signal it: exit once the test removes the
	// directory, or after a ceiling in case the test never does.
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(dir); err != nil {
			return 0
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0
}

// spawnEnv prepares a short directory for the socket and database, points
// the fake daemon at the given mode, and returns the socket path, the
// spawn log, and the test binary to spawn. $BRO_SOCK is cleared on
// purpose: the client must pass the socket path to the daemon it spawns.
func spawnEnv(t *testing.T, mode string) (sock, spawnLog, exe string) {
	t.Helper()

	dir, err := os.MkdirTemp("", "bro")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	exe, err = os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	spawnLog = filepath.Join(dir, "spawns")
	t.Setenv("BRO_SOCK", "")
	t.Setenv("BRO_DB", filepath.Join(dir, "data.db"))
	t.Setenv("BRO_TEST_FAKE_DAEMON", mode)
	t.Setenv("BRO_TEST_SPAWN_LOG", spawnLog)

	return filepath.Join(dir, "s.sock"), spawnLog, exe
}

// spawns counts how many times a daemon was started.
func spawns(t *testing.T, spawnLog string) int {
	t.Helper()

	f, err := os.Open(spawnLog)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("open spawn log: %v", err)
	}
	defer f.Close()

	n := 0
	for sc := bufio.NewScanner(f); sc.Scan(); {
		n++
	}
	return n
}

func TestACallSpawnsTheDaemonWhenNoSocketExists(t *testing.T) {
	sock, spawnLog, exe := spawnEnv(t, "serve")
	c := client.New(sock, exe, testVersion)

	msgs, err := c.Log(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Log with no daemon running: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("messages = %+v, want none from a fresh daemon", msgs)
	}
	if n := spawns(t, spawnLog); n != 1 {
		t.Errorf("spawned %d daemons, want 1", n)
	}
}

func TestACallSpawnsTheDaemonOverAStaleSocketFile(t *testing.T) {
	sock, _, exe := spawnEnv(t, "serve")

	// A socket file nobody listens on: dialing it is refused, not missing.
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()

	c := client.New(sock, exe, testVersion)
	if _, err := c.Log(t.Context(), "alpha"); err != nil {
		t.Fatalf("Log over a stale socket file: %v", err)
	}
}

func TestLaterCallsReuseTheSpawnedDaemon(t *testing.T) {
	sock, spawnLog, exe := spawnEnv(t, "serve")
	c := client.New(sock, exe, testVersion)
	ctx := t.Context()

	if _, err := c.Register(ctx, "alpha", "worker", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := c.Tell(ctx, "alpha", "worker", "persisted", nil, store.KindNote); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	// A second client, as a second CLI invocation would be.
	msgs, err := client.New(sock, exe, testVersion).Log(ctx, "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Body != "persisted" {
		t.Errorf("messages = %+v, want the one message", msgs)
	}
	if n := spawns(t, spawnLog); n != 1 {
		t.Errorf("spawned %d daemons across three calls, want 1", n)
	}
}

func TestConcurrentCallsAllReachADaemon(t *testing.T) {
	sock, _, exe := spawnEnv(t, "serve")

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = client.New(sock, exe, testVersion).Log(t.Context(), "alpha")
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: %v", i, err)
		}
	}
}

func TestACallGivesUpWhenTheDaemonNeverListens(t *testing.T) {
	sock, spawnLog, exe := spawnEnv(t, "die")
	c := client.New(sock, exe, testVersion)

	start := time.Now()
	_, err := c.Log(t.Context(), "alpha")
	if err == nil {
		t.Fatal("Log succeeded with no daemon listening")
	}
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Errorf("gave up after %v; the retry budget must be bounded", elapsed)
	}
	if n := spawns(t, spawnLog); n != 1 {
		t.Errorf("spawned %d daemons in one call, want 1: spawn once, then only redial", n)
	}
}

func TestACallStopsWaitingWhenItsContextEnds(t *testing.T) {
	sock, _, exe := spawnEnv(t, "die")
	c := client.New(sock, exe, testVersion)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.Log(ctx, "alpha")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("returned after %v; a 200ms deadline must cut the retries short", elapsed)
	}
}

func TestACallFailsAtOnceWhenTheDaemonCannotStart(t *testing.T) {
	sock, _, _ := spawnEnv(t, "serve")
	c := client.New(sock, filepath.Join(t.TempDir(), "no-such-bro"), testVersion)

	start := time.Now()
	if _, err := c.Log(t.Context(), "alpha"); err == nil {
		t.Fatal("Log succeeded with an executable that does not exist")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("returned after %v; a failed spawn has nothing to wait for", elapsed)
	}
}

func TestAnEmptyExecutableNeverSpawns(t *testing.T) {
	sock, spawnLog, _ := spawnEnv(t, "serve")
	c := client.New(sock, "", testVersion)

	if _, err := c.Log(t.Context(), "alpha"); err == nil {
		t.Fatal("Log succeeded with no daemon and no executable to spawn")
	}
	if n := spawns(t, spawnLog); n != 0 {
		t.Errorf("spawned %d daemons with exe \"\", want 0", n)
	}
}

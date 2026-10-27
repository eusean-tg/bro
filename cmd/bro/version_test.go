package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eusean-tg/bro/internal/client"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

func TestBuildVersionIsTheSameForTheSameBytes(t *testing.T) {
	a, err := buildVersion(writeFile(t, "build one"))
	if err != nil {
		t.Fatalf("buildVersion: %v", err)
	}
	b, err := buildVersion(writeFile(t, "build one"))
	if err != nil {
		t.Fatalf("buildVersion: %v", err)
	}
	if a == "" || a != b {
		t.Errorf("versions %q and %q, want equal and non-empty for identical files", a, b)
	}
}

func TestBuildVersionChangesWhenTheBytesChange(t *testing.T) {
	a, err := buildVersion(writeFile(t, "build one"))
	if err != nil {
		t.Fatalf("buildVersion: %v", err)
	}
	b, err := buildVersion(writeFile(t, "build two"))
	if err != nil {
		t.Fatalf("buildVersion: %v", err)
	}
	if a == b {
		t.Errorf("both builds have version %q; a rebuild must change it", a)
	}
}

func TestBuildVersionOfAMissingFileIsAnError(t *testing.T) {
	if _, err := buildVersion(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Error("buildVersion succeeded on a file that does not exist")
	}
}

func TestBuildVersionIsTheShortForm(t *testing.T) {
	v, err := buildVersion(writeFile(t, "build one"))
	if err != nil {
		t.Fatalf("buildVersion: %v", err)
	}

	// The header, the daemon's version and its re-check must all compare
	// this one form, so the trim happens here and nowhere else.
	if len(v) != 12 || strings.Trim(v, "0123456789abcdef") != "" {
		t.Errorf("buildVersion = %q, want 12 lowercase hex characters", v)
	}
}

func version(t *testing.T, exe string) string {
	t.Helper()

	v, err := buildVersion(exe)
	if err != nil {
		t.Fatalf("buildVersion(%s): %v", exe, err)
	}
	return v
}

func TestExeChangedIsFalseWhileTheBinaryIsUnchanged(t *testing.T) {
	exe := writeFile(t, "build one")

	if exeChanged(exe, version(t, exe)) {
		t.Error("exeChanged = true for the binary the version was taken from")
	}
}

func TestExeChangedIsTrueOnceTheBinaryIsRebuilt(t *testing.T) {
	exe := writeFile(t, "build one")
	v := version(t, exe)

	if err := os.WriteFile(exe, []byte("build two"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !exeChanged(exe, v) {
		t.Error("exeChanged = false after the binary at the same path was rebuilt")
	}
}

func TestExeChangedIsTrueWhenTheBinaryIsGone(t *testing.T) {
	exe := writeFile(t, "build one")
	v := version(t, exe)

	// go run deletes its build when it exits. A daemon that cannot re-hash
	// its binary cannot show it is current, so it yields.
	if err := os.Remove(exe); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !exeChanged(exe, v) {
		t.Error("exeChanged = false for a binary that no longer exists")
	}
}

func TestACurrentDaemonServesAnOlderClient(t *testing.T) {
	_, sock, db := daemonPaths(t)
	cur := version(t, testExe(t))
	startDaemonAt(t, sock, db, cur)

	// A worker started before the rebuild still sends the old version. Its
	// client has no exe, so it cannot spawn: if the daemon retired, the
	// call fails instead of quietly reaching a replacement.
	if _, err := client.New(sock, "", "old").Log(t.Context(), "alpha"); err != nil {
		t.Fatalf("the current daemon refused an older client: %v", err)
	}
	if _, err := client.New(sock, "", cur).Log(t.Context(), "alpha"); err != nil {
		t.Errorf("the current daemon stopped serving after an older client's request: %v", err)
	}
}

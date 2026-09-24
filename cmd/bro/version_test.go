package main

import (
	"os"
	"path/filepath"
	"testing"
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

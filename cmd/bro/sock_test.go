package main

import (
	"path/filepath"
	"testing"
)

func TestSockPathHonorsBRO_SOCK(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom.sock")
	t.Setenv("BRO_SOCK", want)

	got, err := sockPath()
	if err != nil {
		t.Fatalf("sockPath: %v", err)
	}
	if got != want {
		t.Errorf("sockPath = %q, want %q", got, want)
	}
}

func TestSockPathDefaultsBesideTheDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BRO_SOCK", "")

	got, err := sockPath()
	if err != nil {
		t.Fatalf("sockPath: %v", err)
	}
	if want := filepath.Join(home, ".bro", "bro.sock"); got != want {
		t.Errorf("sockPath = %q, want %q", got, want)
	}
}

package main

import (
	"bytes"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

func TestRunTellAddressesACommaSeparatedList(t *testing.T) {
	s := seeded(t)
	ctx := t.Context()
	for _, name := range []string{"sender", "reader", "other"} {
		if _, err := s.Register(ctx, "alpha", name, ""); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}
	cfg := Config{Identity: "sender", Workspace: "alpha"}

	var out bytes.Buffer
	if err := runTell(ctx, s, cfg, &out, []string{"--to", "reader,other", "heads up"}); err != nil {
		t.Fatalf("runTell: %v", err)
	}

	for _, me := range []string{"reader", "other"} {
		res, err := s.Pull(ctx, "alpha", me, store.PullOptions{})
		if err != nil {
			t.Fatalf("Pull(%s): %v", me, err)
		}
		msgs := res.Messages
		if len(msgs) != 1 || !msgs[0].Mentioned {
			t.Errorf("%s was not addressed by --to reader,other", me)
		}
	}
}

func TestRunTellRejectsAnUnknownRecipient(t *testing.T) {
	s := seeded(t)
	ctx := t.Context()
	if _, err := s.Register(ctx, "alpha", "sender", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	cfg := Config{Identity: "sender", Workspace: "alpha"}

	var out bytes.Buffer
	if err := runTell(ctx, s, cfg, &out, []string{"--to", "ghost", "hi"}); err == nil {
		t.Fatal("runTell accepted an unknown recipient, want an error")
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q on the error path, want nothing", out.String())
	}
}

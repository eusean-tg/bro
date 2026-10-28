package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/eusean-tg/bro/internal/store"
)

const hookType = "bro-worker"

// hookRoom registers the orchestrator, writer, and agent-1's mailbox in
// alpha. The config carries the orchestrator's identity, as every
// subagent's environment does, so a hook that used it would pull as the
// wrong mailbox.
func hookRoom(t *testing.T) (s *store.Store, cfg Config, worker string) {
	t.Helper()

	s = seeded(t)
	worker = mailboxName(hookType, "agent-1")
	for _, name := range []string{"orchestrator", "writer", worker} {
		if _, err := s.Register(t.Context(), "alpha", name, ""); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}
	return s, Config{Identity: "orchestrator", Workspace: "alpha"}, worker
}

// stopInput is agent-1's SubagentStop input. stop_hook_active is true
// because a worker the hook kept alive stops again with it set, and the
// hook must keep blocking regardless.
func stopInput(last string) io.Reader {
	b, _ := json.Marshal(map[string]any{
		"session_id":             "session-1",
		"hook_event_name":        "SubagentStop",
		"stop_hook_active":       true,
		"agent_id":               "agent-1",
		"agent_type":             hookType,
		"last_assistant_message": last,
	})
	return bytes.NewReader(b)
}

func tellAs(t *testing.T, s *store.Store, sender, body string, kind store.Kind, to ...string) {
	t.Helper()

	if _, err := s.Tell(t.Context(), "alpha", sender, body, to, kind); err != nil {
		t.Fatalf("Tell(%s from %s): %v", body, sender, err)
	}
}

type decision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// hook runs `pull --wait WAIT --hook` with in as stdin, failing on any
// error: every outcome but a failure must exit 0. It returns the decoded
// decision and the raw output.
func hook(t *testing.T, s *store.Store, cfg Config, in io.Reader, wait string) (decision, string) {
	t.Helper()

	var out bytes.Buffer
	if err := runPull(t.Context(), clientFor(t, s), cfg, in, &out, []string{"--wait", wait, "--hook"}); err != nil {
		t.Fatalf("runPull --hook = %v, want nil; a hook that exits non-zero shows an error instead of deciding", err)
	}
	if out.Len() == 0 {
		return decision{}, ""
	}
	var d decision
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatalf("hook output is not one JSON object: %v\ngot:\n%s", err, out.String())
	}
	return d, out.String()
}

func TestMailboxNameIsTheAgentTypeAndAShortHashOfItsID(t *testing.T) {
	sum := sha256.Sum256([]byte("agent-1"))
	want := "bro-worker-" + hex.EncodeToString(sum[:])[:8]

	if got := mailboxName("bro-worker", "agent-1"); got != want {
		t.Errorf("mailboxName = %q, want %q", got, want)
	}
	if mailboxName("bro-worker", "agent-2") == want {
		t.Error("two agents of the same type got the same mailbox name")
	}
}

func TestHookDeliversMessagesFramedAsReports(t *testing.T) {
	s, cfg, worker := hookRoom(t)
	tellAs(t, s, "writer", "split the parser", store.KindNote)
	tellAs(t, s, "writer", "take the lexer", store.KindNote, worker)
	tellAs(t, s, "writer", "main is broken, stop", store.KindControl)

	d, _ := hook(t, s, cfg, stopInput(""), "1m")
	if d.Decision != "block" {
		t.Errorf("decision = %q, want block: the worker continues with its messages", d.Decision)
	}
	want := "writer reported: split the parser\n" +
		"writer reported to you: take the lexer\n" +
		"writer announced: main is broken, stop"
	if d.Reason != want {
		t.Errorf("reason:\n%s\nwant:\n%s", d.Reason, want)
	}
}

func TestHookPullsAsTheAgentNotTheInheritedIdentity(t *testing.T) {
	s, cfg, worker := hookRoom(t)
	tellAs(t, s, "writer", "for the worker", store.KindNote, worker)

	d, _ := hook(t, s, cfg, stopInput(""), "2s")
	if d.Reason != "writer reported to you: for the worker" {
		t.Errorf("reason = %q, want the mention delivered to the worker", d.Reason)
	}

	res, err := s.Pull(t.Context(), "alpha", "orchestrator", store.PullOptions{Peek: true})
	if err != nil {
		t.Fatalf("Pull(orchestrator): %v", err)
	}
	if len(res.Messages) != 1 {
		t.Errorf("orchestrator has %d unread, want 1: the hook moved the orchestrator's cursor", len(res.Messages))
	}
}

func TestHookTimeoutWithNothingNewBlocksToKeepWaiting(t *testing.T) {
	s, cfg, _ := hookRoom(t)

	d, _ := hook(t, s, cfg, stopInput(""), "50ms")
	if d.Decision != "block" || d.Reason != "No new messages. Stop again to keep waiting." {
		t.Errorf("decision = %+v, want a block that sends the worker back to wait", d)
	}
}

func TestHookTimeoutStillDeliversWhatArrived(t *testing.T) {
	s, cfg, _ := hookRoom(t)
	// A note to the room wakes nobody, so the wait runs out; the pull
	// after it still returns the note.
	tellAs(t, s, "writer", "to the room", store.KindNote)

	d, _ := hook(t, s, cfg, stopInput(""), "50ms")
	if d.Decision != "block" || d.Reason != "writer reported: to the room" {
		t.Errorf("decision = %+v, want a block carrying the note", d)
	}
}

func TestHookLetsAClosedWorkerStop(t *testing.T) {
	s, cfg, _ := hookRoom(t)
	if _, err := s.CloseWorkspace(t.Context(), "alpha", "orchestrator", "unit complete"); err != nil {
		t.Fatalf("CloseWorkspace: %v", err)
	}

	if _, raw := hook(t, s, cfg, stopInput(""), "1m"); raw != "" {
		t.Errorf("hook printed %q for a closed worker; printing nothing lets it stop", raw)
	}
}

func TestHookPostsTheWorkersSummary(t *testing.T) {
	s, cfg, worker := hookRoom(t)

	d, _ := hook(t, s, cfg, stopInput("refactored the auth middleware"), "50ms")

	msgs, err := s.Log(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Log has %d messages, want the summary", len(msgs))
	}
	if m := msgs[0]; m.Sender != worker || m.Kind != "note" || m.Body != "refactored the auth middleware" {
		t.Errorf("stored %+v, want a note from %s carrying last_assistant_message", m, worker)
	}
	if strings.Contains(d.Reason, "refactored") {
		t.Error("the worker's own summary was delivered back to it")
	}
	// A summary names nobody, so it wakes nobody.
	if woke, err := s.Wakeable(t.Context(), "alpha", "writer"); err != nil || woke {
		t.Errorf("Wakeable(writer) = %v, %v after a summary; want false", woke, err)
	}
}

func TestHookPostsNoSummaryForABlankMessage(t *testing.T) {
	s, cfg, _ := hookRoom(t)

	hook(t, s, cfg, stopInput(" \n "), "50ms")

	if msgs, _ := s.Log(t.Context(), "alpha"); len(msgs) != 0 {
		t.Errorf("a blank last_assistant_message posted %+v", msgs)
	}
}

func TestHookInputMustNameTheAgent(t *testing.T) {
	s, cfg, _ := hookRoom(t)
	// Register the names a missing field would produce, so only a check
	// on the input itself can reject these.
	for _, name := range []string{mailboxName(hookType, ""), mailboxName("", "agent-1")} {
		if _, err := s.Register(t.Context(), "alpha", name, ""); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}

	for _, in := range []string{
		`{"agent_type":"bro-worker"}`,
		`{"agent_id":"agent-1"}`,
		`not json`,
	} {
		var out bytes.Buffer
		err := runPull(t.Context(), clientFor(t, s), cfg, strings.NewReader(in), &out, []string{"--wait", "50ms", "--hook"})
		if err == nil {
			t.Errorf("stdin %s: err = nil, want an error; agents with no id or type would share a mailbox", in)
		}
		if out.Len() != 0 {
			t.Errorf("stdin %s: printed %q, want no decision", in, out.String())
		}
	}
}

func TestHookTakesOnlyWait(t *testing.T) {
	s, cfg, _ := hookRoom(t)

	for _, args := range [][]string{
		{"--hook"},                            // answers at once; the block cap runs out in seconds
		{"--wait", "50ms", "--hook", "--peek"}, // redelivers the same messages on every stop
		{"--wait", "50ms", "--hook", "--json"}, // the decision is the output
	} {
		var out bytes.Buffer
		if err := runPull(t.Context(), clientFor(t, s), cfg, stopInput(""), &out, args); err == nil {
			t.Errorf("runPull %q: err = nil, want an error", args)
		}
	}
}

func TestHookReasonStaysUnderClaudeCodesCap(t *testing.T) {
	s, cfg, worker := hookRoom(t)
	long := strings.Repeat("é→", 300) // 600 characters, 1500 bytes
	for range 30 {
		tellAs(t, s, "writer", long, store.KindNote, worker)
	}

	d, _ := hook(t, s, cfg, stopInput(""), "1m")
	if n := utf8.RuneCountInString(d.Reason); n > 10000 {
		t.Errorf("reason is %d characters; Claude Code caps hook output at 10,000", n)
	}
	// encoding/json turns invalid UTF-8 into U+FFFD, so a character cut in
	// half shows up as one.
	if strings.ContainsRune(d.Reason, utf8.RuneError) {
		t.Error("reason contains U+FFFD: a character was cut in half")
	}
	if !strings.Contains(d.Reason, "bro log") {
		t.Error("a cut reason does not point at bro log for the rest")
	}
}

func TestHookReasonFramesEachKindOfMessage(t *testing.T) {
	msgs := []store.Message{
		{Sender: "writer", Kind: "note", Body: "split the parser"},
		{Sender: "writer", Kind: "note", Body: "take the lexer", Mentioned: true},
		{Sender: "lead", Kind: "control", Body: "main is broken, stop", Mentioned: true},
	}

	want := "writer reported: split the parser\n" +
		"writer reported to you: take the lexer\n" +
		"lead announced: main is broken, stop"
	if got := hookReason(msgs, 9000); got != want {
		t.Errorf("hookReason:\n%s\nwant:\n%s", got, want)
	}
}

func TestHookReasonCutsToExactlyTheBudgetOnACharacterBoundary(t *testing.T) {
	msgs := []store.Message{
		{Sender: "writer", Kind: "note", Body: strings.Repeat("é", 60)}, // 2 bytes each
		{Sender: "writer", Kind: "note", Body: strings.Repeat("→", 60)}, // 3 bytes each
	}
	full := hookReason(msgs, 10000)
	size := utf8.RuneCountInString(full)

	if got := hookReason(msgs, size); got != full {
		t.Errorf("a reason exactly at the budget was cut:\n%s", got)
	}
	// Every budget below the full size lands the cut at a different byte
	// offset, inside a 2-byte or a 3-byte character.
	for budget := 80; budget < size; budget++ {
		got := hookReason(msgs, budget)
		if !utf8.ValidString(got) {
			t.Fatalf("budget %d: cut through a character: %q", budget, got)
		}
		if n := utf8.RuneCountInString(got); n != budget {
			t.Fatalf("budget %d: reason is %d characters, want exactly %d", budget, n, budget)
		}
		if !strings.HasPrefix(got, "writer reported: é") {
			t.Fatalf("budget %d: reason does not start with the oldest message: %q", budget, got)
		}
		if !strings.Contains(got, "bro log") {
			t.Fatalf("budget %d: a cut reason does not point at bro log for the rest", budget)
		}
	}
}

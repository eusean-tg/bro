package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/eusean-tg/bro/internal/store"
)

// startInput is agent-1's SubagentStart input: the same agent stopInput
// describes, so both hooks must arrive at the same mailbox.
func startInput() io.Reader {
	b, _ := json.Marshal(map[string]any{
		"session_id":      "session-1",
		"hook_event_name": "SubagentStart",
		"agent_id":        "agent-1",
		"agent_type":      hookType,
	})
	return bytes.NewReader(b)
}

type startOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// startRoom has the orchestrator registered, and a config carrying its
// identity, as every subagent's environment does.
func startRoom(t *testing.T) (*store.Store, Config) {
	t.Helper()

	s := seeded(t)
	if _, err := s.Register(t.Context(), "alpha", "orchestrator", "coordinates the unit"); err != nil {
		t.Fatalf("Register(orchestrator): %v", err)
	}
	return s, Config{Identity: "orchestrator", Workspace: "alpha"}
}

// hookStart runs `hook-start` with in as stdin, failing on any error, and
// returns the decoded output.
func hookStart(t *testing.T, s *store.Store, cfg Config, in io.Reader) startOutput {
	t.Helper()

	var out bytes.Buffer
	if err := runHookStart(t.Context(), clientFor(t, s), cfg, in, &out); err != nil {
		t.Fatalf("runHookStart = %v, want nil", err)
	}
	var o startOutput
	if err := json.Unmarshal(out.Bytes(), &o); err != nil {
		t.Fatalf("hook-start output is not one JSON object: %v\ngot:\n%s", err, out.String())
	}
	return o
}

func TestHookStartRegistersTheAgentsOwnMailbox(t *testing.T) {
	s, cfg := startRoom(t)

	hookStart(t, s, cfg, startInput())

	if _, err := s.Mailbox(t.Context(), "alpha", mailboxName(hookType, "agent-1")); err != nil {
		t.Errorf("the agent's mailbox was not registered: %v", err)
	}
}

func TestHookStartAndTheStopHookShareAMailbox(t *testing.T) {
	s, cfg := startRoom(t)
	if _, err := s.Register(t.Context(), "alpha", "writer", ""); err != nil {
		t.Fatalf("Register(writer): %v", err)
	}

	hookStart(t, s, cfg, startInput())
	tellAs(t, s, "writer", "take the lexer", store.KindNote, mailboxName(hookType, "agent-1"))

	if d, _ := hook(t, s, cfg, stopInput(""), "2s"); d.Reason != "writer reported to you: take the lexer" {
		t.Errorf("stop hook reason = %q, want the message sent to the mailbox hook-start registered", d.Reason)
	}
}

func TestHookStartTwiceIsHarmless(t *testing.T) {
	s, cfg := startRoom(t)

	hookStart(t, s, cfg, startInput())
	o := hookStart(t, s, cfg, startInput())

	if worker := mailboxName(hookType, "agent-1"); !strings.Contains(o.HookSpecificOutput.AdditionalContext, worker) {
		t.Errorf("second run's context does not name %s:\n%s", worker, o.HookSpecificOutput.AdditionalContext)
	}
}

func TestHookStartAnswersWithTheAgentsNameAndTheRoster(t *testing.T) {
	s, cfg := startRoom(t)

	o := hookStart(t, s, cfg, startInput())

	if got := o.HookSpecificOutput.HookEventName; got != "SubagentStart" {
		t.Errorf("hookEventName = %q, want SubagentStart: Claude Code ignores output for another event", got)
	}
	for _, want := range []string{mailboxName(hookType, "agent-1"), "orchestrator", "coordinates the unit"} {
		if !strings.Contains(o.HookSpecificOutput.AdditionalContext, want) {
			t.Errorf("context does not mention %q:\n%s", want, o.HookSpecificOutput.AdditionalContext)
		}
	}
}

func TestHookStartInputMustNameTheAgent(t *testing.T) {
	s := seeded(t)
	cfg := Config{Identity: "orchestrator", Workspace: "alpha"}

	for _, in := range []string{
		`{"agent_type":"bro-worker"}`,
		`{"agent_id":"agent-1"}`,
		`not json`,
	} {
		var out bytes.Buffer
		if err := runHookStart(t.Context(), clientFor(t, s), cfg, strings.NewReader(in), &out); err == nil {
			t.Errorf("stdin %s: err = nil, want an error; agents with no id or type would share a mailbox", in)
		}
		if out.Len() != 0 {
			t.Errorf("stdin %s: printed %q, want nothing", in, out.String())
		}
	}

	if roster, err := s.Mailboxes(t.Context(), "alpha"); err != nil || len(roster) != 0 {
		t.Errorf("Mailboxes = %+v, %v; want nothing registered from bad input", roster, err)
	}
}

const selfName = "bro-worker-3fa9c1d2"

func TestHookContextGivesTheAgentItsNameAndTheCommandToPost(t *testing.T) {
	got := hookContext("alpha", selfName, nil)

	for _, want := range []string{"alpha", "bro --as " + selfName + " tell"} {
		if !strings.Contains(got, want) {
			t.Errorf("context does not contain %q:\n%s", want, got)
		}
	}
}

func TestHookContextListsEveryoneElseWithTheirDescription(t *testing.T) {
	roster := []store.Mailbox{
		{Name: "orchestrator", Description: "coordinates the unit"},
		{Name: "writer-7", Description: "owns the parser"},
		{Name: "lexer-2"},
	}

	got := hookContext("alpha", selfName, roster)

	for _, mb := range roster {
		line := lineWith(got, mb.Name)
		if line == "" {
			t.Errorf("roster is missing %s:\n%s", mb.Name, got)
			continue
		}
		if !strings.Contains(line, mb.Description) {
			t.Errorf("line for %s is %q, want its description %q on the same line", mb.Name, line, mb.Description)
		}
	}
}

func TestHookContextLeavesTheAgentOutOfItsOwnRoster(t *testing.T) {
	others := []store.Mailbox{{Name: "orchestrator", Description: "coordinates the unit"}}
	// The roster hook-start fetches is read after registering, so it
	// always includes the agent itself, first here by name order.
	withMe := []store.Mailbox{{Name: selfName, Description: "listed as someone else"}, others[0]}

	if got, want := hookContext("alpha", selfName, withMe), hookContext("alpha", selfName, others); got != want {
		t.Errorf("an agent in the roster changed the context:\n%s\nwant:\n%s", got, want)
	}
}

// lineWith returns the first line of s that contains sub, or "".
func lineWith(s, sub string) string {
	for line := range strings.Lines(s) {
		if strings.Contains(line, sub) {
			return line
		}
	}
	return ""
}

package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/eusean-tg/bro/internal/client"
	"github.com/eusean-tg/bro/internal/store"
)

var (
	errTimedOut = errors.New("timed out")
	errClosed   = errors.New("closed")
)

func exitCode(err error) int {
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errTimedOut):
		return 3
	case errors.Is(err, errClosed):
		return 4
	default:
		return 1
	}
}

func dbPath() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}

	v := cmp.Or(os.Getenv("BRO_DB"), filepath.Join(h, ".bro", "data.db"))

	if err := os.MkdirAll(filepath.Dir(v), 0o700); err != nil {
		return "", fmt.Errorf("create db at path: %w", err)
	}

	return v, nil
}

func sockPath() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}

	v := cmp.Or(os.Getenv("BRO_SOCK"), filepath.Join(h, ".bro", "bro.sock"))

	if err := os.MkdirAll(filepath.Dir(v), 0o700); err != nil {
		return "", fmt.Errorf("create sock at path: %w", err)
	}

	return v, nil
}

func Resolve(asFlag, wsFlag string) (Config, error) {
	var cfg Config
	cfg.Identity = cmp.Or(asFlag, os.Getenv("BRO_IDENTITY"))
	cfg.Workspace = cmp.Or(wsFlag, os.Getenv("BRO_WORKSPACE"))

	if cfg.Workspace == "" {
		return Config{}, fmt.Errorf("resolve workspace: %w", ErrNoWorkspace)
	}

	return cfg, nil
}

func main() {
	err := run(os.Args[1:])
	code := exitCode(err)

	if code == 1 {
		fmt.Fprintln(os.Stderr, "bro:", err)
	}

	os.Exit(code)
}

func run(args []string) error {
	ctx := context.Background()
	global := flag.NewFlagSet("bro", flag.ContinueOnError)
	workspace := global.String("workspace", "", "workspace to work in")
	identity := global.String("as", "", "identity for the message")

	if err := global.Parse(args); err != nil {
		return err
	}

	if global.NArg() == 0 {
		return errors.New("usage: bro [--workspace NAME] [--as NAME] <tell|log|register|pull|daemon|close|hook-start>")
	}

	sock, err := sockPath()
	if err != nil {
		return fmt.Errorf("sock path: %w", err)
	}
	db, err := dbPath()
	if err != nil {
		return fmt.Errorf("db path: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("exe path: %w", err)
	}

	ver, err := buildVersion(exe)
	if err != nil {
		return fmt.Errorf("build version: %w", err)
	}

	if global.Arg(0) == "daemon" {
		return runDaemon(ctx, sock, db, exe, ver)
	}

	cfg, err := Resolve(*identity, *workspace)
	if err != nil {
		return err
	}

	c := client.New(sock, exe, ver)

	switch cmd, rest := global.Arg(0), global.Args()[1:]; cmd {
	case "tell":
		return runTell(ctx, c, cfg, os.Stdout, rest)
	case "log":
		return runLog(ctx, c, cfg, os.Stdout)
	case "register":
		return runRegister(ctx, c, cfg, os.Stdout, rest)
	case "pull":
		return runPull(ctx, c, cfg, os.Stdin, os.Stdout, rest)
	case "close":
		return runClose(ctx, c, cfg, os.Stdout, rest)
	case "hook-start":
		return runHookStart(ctx, c, cfg, os.Stdin, os.Stdout)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func runTell(
	ctx context.Context,
	c *client.Client,
	cfg Config,
	out io.Writer,
	rest []string,
) error {
	tell := flag.NewFlagSet("tell", flag.ContinueOnError)
	toFlag := tell.String("to", "", "name(s) for recipient, comma-separated: alice,bob")
	kindFlag := tell.String("kind", "note", `kind of message, either "control" or "note"; defaults to "note"`)

	if err := tell.Parse(rest); err != nil {
		return err
	}

	if tell.NArg() == 0 {
		return errors.New("no message")
	}

	if tell.NArg() > 1 {
		return errors.New("too many arguments")
	}

	if cfg.Identity == "" {
		return errors.New("tell requires identity")
	}

	var to []string
	if *toFlag != "" {
		to = strings.Split(*toFlag, ",")
	}

	id, err := c.Tell(ctx, cfg.Workspace, cfg.Identity, tell.Arg(0), to, store.Kind(*kindFlag))
	if err != nil {
		return fmt.Errorf("unable to tell: %w", err)
	}

	_, _ = fmt.Fprintf(out, "%d\n", id)

	return nil
}

func runClose(
	ctx context.Context,
	c *client.Client,
	cfg Config,
	out io.Writer,
	rest []string,
) error {
	cls := flag.NewFlagSet("close", flag.ContinueOnError)

	if err := cls.Parse(rest); err != nil {
		return err
	}

	msg := "unit complete"
	if cls.NArg() > 0 {
		a := strings.TrimSpace(cls.Arg(0))
		if a != "" {
			msg = a
		}
	}

	if cfg.Identity == "" {
		return errors.New("close requires identity")
	}

	id, err := c.CloseWorkspace(ctx, cfg.Workspace, cfg.Identity, msg)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "%d\n", id)

	return nil
}

func printMessages(out io.Writer, msgs []store.Message) {
	var w int

	for _, m := range msgs {
		w = max(w, utf8.RuneCountInString(m.Sender))
	}

	for _, m := range msgs {
		marker := ""

		if m.Kind == "control" {
			marker += "*"
		}

		if m.Mentioned {
			marker += "@"
		}

		head := fmt.Sprintf(
			"%s%4d %s %-*s ",
			marker,
			m.ID,
			time.Unix(m.CreatedAt, 0).Format(time.TimeOnly),
			w,
			m.Sender,
		)

		pad := strings.Repeat(" ", utf8.RuneCountInString(head))

		lines := strings.Split(strings.TrimRight(m.Body, "\n"), "\n")

		for i, line := range lines {
			prefix := head
			if i > 0 {
				prefix = pad
			}
			_, _ = fmt.Fprintf(out, "%s> %s\n", prefix, line)
		}
	}
}

func runLog(ctx context.Context, c *client.Client, cfg Config, out io.Writer) error {
	msgs, err := c.Log(ctx, cfg.Workspace, store.LogOptions{}, 0)
	if err != nil {
		return fmt.Errorf("log messages: %w", err)
	}

	printMessages(out, msgs)

	return nil
}

func randomName() string {
	return fmt.Sprintf("worker-%04x", rand.N(0x10000))
}

func runRegister(
	ctx context.Context,
	c *client.Client,
	cfg Config,
	out io.Writer,
	rest []string,
) error {
	register := flag.NewFlagSet("register", flag.ContinueOnError)
	n := register.String("n", "", "name for mailbox")
	d := register.String("d", "", "short description for who the receiver")

	if err := register.Parse(rest); err != nil {
		return err
	}

	name := cmp.Or(*n, cfg.Identity, randomName())

	mb, err := c.Register(ctx, cfg.Workspace, name, *d)
	if err != nil {
		return fmt.Errorf("register mailbox: %w", err)
	}

	_, _ = fmt.Fprintln(out, mb.Name)

	return nil
}

type hookInput struct {
	AgentID              string `json:"agent_id"`
	AgentType            string `json:"agent_type"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

func runPull(
	ctx context.Context,
	c *client.Client,
	cfg Config,
	in io.Reader,
	out io.Writer,
	rest []string,
) error {
	pull := flag.NewFlagSet("pull", flag.ContinueOnError)
	snc := pull.Int64("since", 0, "since id N")
	lst := pull.Int("last", 0, "the N most recent")
	pk := pull.Bool("peek", false, "do not advance the cursor")
	js := pull.Bool("json", false, "enables json output")
	wt := pull.Duration("wait", 0, "wait for a mention")
	hk := pull.Bool("hook", false, "stop hook")

	if err := pull.Parse(rest); err != nil {
		return err
	}

	var opts store.PullOptions
	set := map[string]bool{}
	pull.Visit(func(f *flag.Flag) { set[f.Name] = true })

	if set["since"] && set["last"] {
		return errors.New("since and last cannot be used together")
	}

	if set["wait"] && (set["since"] || set["last"]) {
		return errors.New("wait cannot be used together with since or last")
	}

	if set["since"] {
		opts.Since = snc
	}
	if set["last"] {
		opts.Last = *lst
	}
	if set["peek"] {
		opts.Peek = *pk
	}

	var wait time.Duration
	if set["wait"] {
		wait = *wt
	}

	if set["hook"] && *hk {
		if wait <= 0 || !set["wait"] {
			return errors.New("wait is required for hook mode")
		}

		if set["peek"] {
			return errors.New("hook mode cannot be peeked")
		}

		if set["json"] {
			return errors.New("hook mode is already in json")
		}

		return pullForHook(ctx, c, cfg.Workspace, wait, in, out)
	}

	if cfg.Identity == "" {
		return errors.New("pull requires identity")
	}

	res, err := c.Pull(
		ctx,
		cfg.Workspace,
		cfg.Identity,
		opts,
		wait,
	)
	if err != nil {
		return fmt.Errorf("pull messages: %w", err)
	}

	if set["json"] && *js {
		if err := json.NewEncoder(out).Encode(res); err != nil {
			return err
		}
	} else {
		printMessages(out, res.Messages)
	}

	if res.Closed {
		return errClosed
	}

	if res.TimedOut {
		return errTimedOut
	}

	return nil
}

type hookDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

func getHookInput(in io.Reader) (hookInput, error) {
	var input hookInput
	if err := json.NewDecoder(in).Decode(&input); err != nil {
		return hookInput{}, fmt.Errorf("get hook input decode: %w", err)
	}

	if input.AgentID == "" || input.AgentType == "" {
		return hookInput{}, errors.New("agent_id and agent_type must not be empty")
	}

	input.LastAssistantMessage = strings.TrimSpace(input.LastAssistantMessage)

	return input, nil
}

func pullForHook(
	ctx context.Context,
	c *client.Client,
	ws string,
	wait time.Duration,
	in io.Reader,
	out io.Writer,
) error {
	input, err := getHookInput(in)
	if err != nil {
		return fmt.Errorf("pull for hook get input: %w", err)
	}

	mb := mailboxName(input.AgentType, input.AgentID)

	if input.LastAssistantMessage != "" {
		_, err := c.Tell(ctx, ws, mb, input.LastAssistantMessage, nil, store.KindNote)
		if err != nil {
			return fmt.Errorf("pull for hook tell: %w", err)
		}
	}

	res, err := c.Pull(
		ctx,
		ws,
		mb,
		store.PullOptions{},
		wait,
	)
	if err != nil {
		return fmt.Errorf("pull messages: %w", err)
	}

	if res.Closed {
		return nil
	}

	var d hookDecision = hookDecision{Decision: "block", Reason: ""}

	if len(res.Messages) > 0 {
		d.Reason = hookReason(res.Messages, 9000)
	} else {
		d.Reason = "No new messages. Stop again to keep waiting."
	}

	if err := json.NewEncoder(out).Encode(d); err != nil {
		return fmt.Errorf("pull for hook encode: %w", err)
	}

	return nil
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

type hookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

func runHookStart(
	ctx context.Context,
	c *client.Client,
	cfg Config,
	in io.Reader,
	out io.Writer,
) error {
	input, err := getHookInput(in)
	if err != nil {
		return fmt.Errorf("run hook start get input: %w", err)
	}

	mbn := mailboxName(input.AgentType, input.AgentID)

	_, err = c.Register(ctx, cfg.Workspace, mbn, "")
	if err != nil && !errors.Is(err, store.ErrMailboxExists) {
		return fmt.Errorf("register mailbox: %w", err)
	}

	mbs, err := c.Mailboxes(ctx, cfg.Workspace)
	if err != nil {
		return fmt.Errorf("fetch roster: %w", err)
	}

	o := hookOutput{
		HookSpecificOutput: hookSpecificOutput{
			HookEventName:     "SubagentStart",
			AdditionalContext: hookContext(cfg.Workspace, mbn, mbs),
		},
	}

	if err := json.NewEncoder(out).Encode(o); err != nil {
		return fmt.Errorf("run hook start encode: %w", err)
	}

	return nil
}

func hookContext(ws, me string, roster []store.Mailbox) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %q in bro workspace %q\n", me, ws)
	fmt.Fprintf(&b, "Post with: bro --as %s tell [--to NAME,NAME] MESSAGE\n", me)
	fmt.Fprintf(&b, "--to wakes the mailboxes you name; everyone can read every message.\n")
	fmt.Fprintf(&b, "Messages for you arrive when you end your turn.\n")
	fmt.Fprintf(&b, "Other mailboxes:\n")

	for _, mb := range roster {
		if mb.Name == me {
			continue
		}

		fmt.Fprintf(&b, "- %s", mb.Name)

		desc := strings.TrimSpace(mb.Description)
		if desc != "" {
			fmt.Fprintf(&b, ": %s", desc)
		}

		fmt.Fprintf(&b, "\n")
	}

	return b.String()
}

func hash(src io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, src); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func buildVersion(exe string) (string, error) {
	f, err := os.Open(exe)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h, err := hash(f)
	if err != nil {
		return "", err
	}

	return h[:12], nil
}

func exeChanged(exe, version string) bool {
	v, err := buildVersion(exe)
	if err != nil {
		return true
	}

	return v != version
}

func mailboxName(agentType, agentID string) string {
	sum := sha256.Sum256([]byte(agentID))
	hexStr := fmt.Sprintf("%x", sum)
	return agentType + "-" + hexStr[:8]
}

func shorten(body string, n int) string {
	r := []rune(body) // like [...name] in JS
	if len(r) <= n {
		return body
	}
	return string(r[:n]) // back to a string
}

func hookReason(msgs []store.Message, budget int) string {
	const hookCut = "\n[cut to fit; run `bro log` for the rest]"
	hookCutN := utf8.RuneCountInString(hookCut)

	lines := make([]string, 0)

	for _, m := range msgs {
		verb := "reported"
		if m.Kind == string(store.KindControl) {
			verb = "announced"
		} else if m.Mentioned {
			verb = "reported to you"
		}

		lines = append(lines, fmt.Sprintf("%s %s: %s", m.Sender, verb, m.Body))
	}

	out := strings.Join(lines, "\n")

	n := utf8.RuneCountInString(out)

	if n <= budget {
		return out
	}

	trunc := shorten(out, budget-hookCutN)
	trunc += hookCut

	return trunc
}

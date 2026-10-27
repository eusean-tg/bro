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
		return errors.New("usage: bro [--workspace NAME] [--as NAME] <tell|log|register|pull|daemon>")
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
		return runPull(ctx, c, cfg, os.Stdout, rest)
	case "close":
		return runClose(ctx, c, cfg, os.Stdout, rest)
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
	msgs, err := c.Log(ctx, cfg.Workspace)
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

func runPull(
	ctx context.Context,
	c *client.Client,
	cfg Config,
	out io.Writer,
	rest []string,
) error {
	pull := flag.NewFlagSet("pull", flag.ContinueOnError)
	snc := pull.Int64("since", 0, "since id N")
	lst := pull.Int("last", 0, "the N most recent")
	pk := pull.Bool("peek", false, "do not advance the cursor")
	js := pull.Bool("json", false, "enables json output")
	wt := pull.Duration("wait", 0, "wait for a mention")

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

func buildVersion(exe string) (string, error) {
	f, err := os.Open(exe)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

func exeChanged(exe, version string) bool {
	v, err := buildVersion(exe)
	if err != nil {
		return true
	}

	return v != version
}

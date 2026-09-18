package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/eusean-tg/bro/internal/store"
)

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
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}

		fmt.Fprintln(os.Stderr, "bro:", err)
		os.Exit(1)
	}
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
		return errors.New("usage: bro [--workspace NAME] [--as NAME] <tell|log>")
	}

	cfg, err := Resolve(*identity, *workspace)
	if err != nil {
		return err
	}

	db, err := dbPath()
	if err != nil {
		return fmt.Errorf("db path: %w", err)
	}

	s, err := store.Open(db)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	switch cmd, rest := global.Arg(0), global.Args()[1:]; cmd {
	case "tell":
		return runTell(ctx, s, cfg, os.Stdout, rest)
	case "log":
		return runLog(ctx, s, cfg, os.Stdout)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func runTell(ctx context.Context, s *store.Store, cfg Config, out io.Writer, rest []string) error {
	if len(rest) == 0 {
		return errors.New("no message")
	}

	if len(rest) > 1 {
		return errors.New("too many arguments")
	}

	if cfg.Identity == "" {
		return errors.New("tell requires identity")
	}

	id, err := s.Tell(ctx, cfg.Workspace, cfg.Identity, rest[0])
	if err != nil {
		return fmt.Errorf("unable to tell: %w", err)
	}

	fmt.Fprintf(out, "%d\n", id)

	return nil
}

func runLog(ctx context.Context, s *store.Store, cfg Config, out io.Writer) error {
	msgs, err := s.Log(ctx, cfg.Workspace)
	if err != nil {
		return fmt.Errorf("log messages: %w", err)
	}

	var w int

	for _, m := range msgs {
		w = max(w, utf8.RuneCountInString(m.Sender))
	}

	for _, m := range msgs {
		marker := " "

		if m.Kind == "control" {
			marker = "*"
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

	return nil
}

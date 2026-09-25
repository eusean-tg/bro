package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type Store struct {
	db  *sql.DB
	Now func() time.Time
}

type Message struct {
	ID        int64  `json:"id"`
	Sender    string `json:"sender"`
	Kind      string `json:"kind"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"created_at"`
	Mentioned bool   `json:"mentioned"`
}

type PullResult struct {
	Messages    []Message `json:"messages"`
	LatestID    int64     `json:"latest_id"`
	UnreadCount int       `json:"unread_count"`
}

var (
	ErrMailboxExists = errors.New("mailbox already exists")
	ErrNoMailbox     = errors.New("mailbox doesn't exist")
)

type Mailbox struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Cursor      int64  `json:"cursor"`
	CreatedAt   int64  `json:"created_at"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	return &Store{db: db, Now: time.Now}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func dedupStringSlice(strings []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(strings)+1)

	for _, val := range strings {
		if _, exists := seen[val]; !exists {
			seen[val] = struct{}{}
			result = append(result, val)
		}
	}
	return result
}

func (s *Store) Tell(
	ctx context.Context,
	ws string,
	sender string,
	body string,
	to []string,
) (int64, error) {
	names := make([]string, 0)
	names = append(names, sender)
	names = append(names, to...)
	names = dedupStringSlice(names)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	mbs, err := fetchMailboxes(ctx, tx, ws, names)
	if err != nil {
		return 0, fmt.Errorf("sender mailbox: %w", err)
	}

	if len(mbs) != len(names) {
		nmap := make(map[string]bool)
		for _, n := range mbs {
			nmap[n.Name] = true
		}

		missing := make([]string, 0)
		for _, n := range names {
			if ok := nmap[n]; !ok {
				missing = append(missing, n)
			}
		}

		return 0, fmt.Errorf(
			"recipients %s: %w",
			strings.Join(missing, ", "),
			ErrNoMailbox,
		)
	}

	var id int64
	if err := tx.QueryRowContext(
		ctx,
		`INSERT INTO messages (workspace, sender, body, created_at) VALUES (?, ?, ?, ?) RETURNING id`,
		ws, sender, body, s.Now().Unix(),
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert message: %w", err)
	}

	for _, v := range to {
		_, err := tx.ExecContext(
			ctx,
			`INSERT INTO mentions (message_id, mailbox) VALUES (?, ?)`,
			id,
			v,
		)
		if err != nil {
			return 0, err
		}
	}

	if err = tx.Commit(); err != nil {
		return 0, err
	}

	return id, nil
}

func (s *Store) Log(ctx context.Context, ws string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, sender, kind, body, created_at FROM messages WHERE workspace = ? ORDER BY id`, ws)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Sender, &m.Kind, &m.Body, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}

	return out, rows.Err()
}

func (s *Store) Register(
	ctx context.Context,
	ws string,
	name string,
	desc string,
) (*Mailbox, error) {
	var mb Mailbox

	err := s.db.QueryRowContext(
		ctx,
		`INSERT INTO mailboxes (workspace, name, description, cursor, created_at) VALUES (?,?,?,COALESCE((SELECT MAX(id) FROM messages WHERE workspace = ?), 0),?) ON CONFLICT (workspace,name) DO NOTHING RETURNING name, description, cursor, created_at`,
		ws,
		name,
		desc,
		ws,
		s.Now().Unix(),
	).Scan(&mb.Name, &mb.Description, &mb.Cursor, &mb.CreatedAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("register %s: %w", name, ErrMailboxExists)
	case err != nil:
		return nil, fmt.Errorf("insert mailbox: %w", err)
	}

	return &mb, nil
}

func (s *Store) Mailbox(
	ctx context.Context,
	ws string,
	name string,
) (*Mailbox, error) {
	return fetchMailbox(ctx, s.db, ws, name)
}

func fetchMailboxes(
	ctx context.Context,
	q querier,
	ws string,
	names []string,
) ([]Mailbox, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	query := `SELECT name, description, cursor, created_at FROM mailboxes WHERE workspace = ? AND name IN (` + placeholders + `)`

	args := make([]any, 0, len(names)+1)
	args = append(args, ws)
	for _, n := range names {
		args = append(args, n)
	}

	rows, err := q.QueryContext(
		ctx,
		query,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query mailboxes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Mailbox
	for rows.Next() {
		var mb Mailbox
		if err := rows.Scan(&mb.Name, &mb.Description, &mb.Cursor, &mb.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan mailbox: %w", err)
		}
		out = append(out, mb)
	}

	return out, rows.Err()
}

func fetchMailbox(
	ctx context.Context,
	q querier,
	ws string,
	name string,
) (*Mailbox, error) {
	var mb Mailbox
	err := q.QueryRowContext(
		ctx,
		`SELECT name, description, cursor, created_at FROM mailboxes WHERE name = ? AND workspace = ?`,
		name,
		ws,
	).Scan(&mb.Name, &mb.Description, &mb.Cursor, &mb.CreatedAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("mailbox %s: %w", name, ErrNoMailbox)
	case err != nil:
		return nil, fmt.Errorf("lookup mailbox: %w", err)
	}

	return &mb, nil
}

type PullOptions struct {
	Since *int64 // nil: start form the cursor
	Last  int    // 0: no limit
	Peek  bool   // false: advance the cursor
}

func (s *Store) Pull(
	ctx context.Context,
	ws string,
	me string,
	opts PullOptions,
) (PullResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PullResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	mb, err := fetchMailbox(ctx, tx, ws, me)
	if err != nil {
		return PullResult{}, err
	}

	cursor := mb.Cursor
	if opts.Since != nil {
		cursor = *opts.Since
	}

	var rows *sql.Rows
	if opts.Last > 0 {
		rows, err = tx.QueryContext(
			ctx,
			`SELECT id, sender, kind, body, created_at, mentioned FROM (
				SELECT id, sender, kind, body, created_at, EXISTS(
					SELECT 1 FROM mentions x WHERE x.message_id = m.id AND x.mailbox = ?
				) AS mentioned FROM messages m
				WHERE workspace = ? AND sender != ?
				ORDER BY id DESC LIMIT ?
			) ORDER BY id ASC`,
			me,
			ws,
			me,
			opts.Last,
		)
		if err != nil {
			return PullResult{}, err
		}
	} else {
		rows, err = tx.QueryContext(
			ctx,
			`SELECT id, sender, kind, body, created_at, EXISTS(
				SELECT 1 FROM mentions x WHERE x.message_id = m.id and x.mailbox = ?
			) AS mentioned FROM messages m
			WHERE workspace = ? AND id > ? AND sender != ? 
			ORDER BY id ASC`,
			me,
			ws,
			cursor,
			me,
		)
		if err != nil {
			return PullResult{}, err
		}
	}

	out := PullResult{
		Messages: []Message{},
	}
	for rows.Next() {
		var m Message
		if err = rows.Scan(&m.ID, &m.Sender, &m.Kind, &m.Body, &m.CreatedAt, &m.Mentioned); err != nil {
			return PullResult{}, err
		}
		out.Messages = append(out.Messages, m)
	}
	_ = rows.Close()
	if rows.Err() != nil {
		return PullResult{}, rows.Err()
	}

	var lastID int64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(id), 0) FROM messages WHERE workspace = ?`,
		ws,
	).Scan(&lastID); err != nil {
		return PullResult{}, err
	}
	out.LatestID = lastID

	plain := !opts.Peek && opts.Since == nil && opts.Last == 0
	if plain {
		out.UnreadCount = 0

		if _, err := tx.ExecContext(
			ctx,
			`UPDATE mailboxes SET cursor = ? WHERE workspace = ? AND name = ?`,
			lastID,
			ws,
			me,
		); err != nil {
			return PullResult{}, err
		}

	} else {
		var uc int
		if err := tx.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM messages WHERE workspace = ? AND id > ? AND sender != ?`,
			ws,
			mb.Cursor,
			me,
		).Scan(&uc); err != nil {
			return PullResult{}, err
		}
		out.UnreadCount = uc
	}

	if err = tx.Commit(); err != nil {
		return PullResult{}, err
	}

	return out, nil
}

func (s *Store) Wakeable(ctx context.Context, ws, me string) (bool, error) {
	mb, err := fetchMailbox(ctx, s.db, ws, me)
	if err != nil {
		return false, fmt.Errorf("wakeable fetch mailbox: %w", err)
	}

	var shouldWake bool
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT EXISTS(
				SELECT 1 FROM messages msg
				JOIN mentions mnt 
				ON msg.id = mnt.message_id
				WHERE msg.workspace = ? AND msg.id > ? AND msg.sender <> ? AND mnt.mailbox = ?
		)`,
		ws,
		mb.Cursor,
		me,
		me,
	).Scan(&shouldWake); err != nil {
		return false, fmt.Errorf("wakeable should wake: %w", err)
	}

	return shouldWake, nil
}

package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	db  *sql.DB
	Now func() time.Time
}

type Message struct {
	ID        int64
	Sender    string
	Kind      string
	Body      string
	CreatedAt int64
}

var (
	ErrMailboxExists = errors.New("mailbox already exists")
	ErrNoMailbox     = errors.New("mailbox doesn't exist")
)

type Mailbox struct {
	Name        string
	Description string
	Cursor      int64
	CreatedAt   int64
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

func (s *Store) Tell(
	ctx context.Context,
	ws string,
	sender string,
	body string,
) (int64, error) {
	var id int64

	_, err := fetchMailbox(ctx, s.db, ws, sender)
	if err != nil {
		return 0, fmt.Errorf("sender mailbox: %w", err)
	}

	if err := s.db.QueryRowContext(
		ctx,
		`INSERT INTO messages (workspace, sender, body, created_at) VALUES (?, ?, ?, ?) RETURNING id`,
		ws, sender, body, s.Now().Unix(),
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert message: %w", err)
	}

	return id, nil
}

func (s *Store) Log(ctx context.Context, ws string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, sender, kind, body, created_at FROM messages WHERE workspace = ? ORDER BY id`, ws)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Message
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

func (s *Store) Pull(
	ctx context.Context,
	ws string,
	me string,
) ([]Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	mb, err := fetchMailbox(ctx, tx, ws, me)
	if err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(
		ctx,
		`SELECT id, sender, kind, body, created_at FROM messages WHERE workspace = ? AND id > ? AND sender != ? ORDER BY id ASC`,
		ws,
		mb.Cursor,
		me,
	)
	if err != nil {
		return nil, err
	}

	out := make([]Message, 0)
	for rows.Next() {
		var m Message
		if err = rows.Scan(&m.ID, &m.Sender, &m.Kind, &m.Body, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	_ = rows.Close()
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET cursor = (SELECT COALESCE(MAX(id), 0) FROM messages WHERE workspace = ?) WHERE workspace = ? AND name = ?`, ws, ws, me); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return out, nil
}

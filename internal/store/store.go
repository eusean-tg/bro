package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

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

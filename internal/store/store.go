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

type Kind string

const (
	KindNote    Kind = "note"
	KindControl Kind = "control"
)

var ErrBadKind = errors.New("invalid kind")

func (k Kind) Valid() bool { return k == KindNote || k == KindControl }

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
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
	Closed      bool      `json:"closed"`
}

var (
	ErrMailboxExists = errors.New("mailbox already exists")
	ErrNoMailbox     = errors.New("mailbox doesn't exist")
)

type Mailbox struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	JoinedID    int64  `json:"joined_id"`
	Cursor      int64  `json:"cursor"`
	CreatedAt   int64  `json:"created_at"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate")
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
	kind Kind,
) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	id, err := writeMessage(ctx, tx, ws, sender, body, to, kind, s.Now().Unix())
	if err != nil {
		return 0, err
	}

	if err = tx.Commit(); err != nil {
		return 0, err
	}

	return id, nil
}

func writeMessage(
	ctx context.Context,
	q querier,
	ws,
	sender,
	body string,
	to []string,
	kind Kind,
	timestamp int64,
) (int64, error) {
	if !kind.Valid() {
		return 0, fmt.Errorf("kind %q: %w", kind, ErrBadKind)
	}

	recipients := dedupStringSlice(to)

	// check sender mailbox existence
	_, err := fetchMailbox(ctx, q, ws, sender)
	if err != nil {
		return 0, fmt.Errorf("sender mailbox: %w", err)
	}

	// check recipient mailbox existences
	mbs, err := fetchMailboxes(ctx, q, ws, recipients)
	if err != nil {
		return 0, fmt.Errorf("recipient mailboxes: %w", err)
	}

	if len(mbs) != len(recipients) {
		nmap := make(map[string]bool)
		for _, n := range mbs {
			nmap[n.Name] = true
		}

		missing := make([]string, 0)
		for _, n := range recipients {
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
	if err := q.QueryRowContext(
		ctx,
		`INSERT INTO messages (workspace, sender, body, kind, created_at) VALUES (?, ?, ?, ?, ?) RETURNING id`,
		ws, sender, body, kind, timestamp,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert message: %w", err)
	}

	for _, v := range recipients {
		_, err := q.ExecContext(
			ctx,
			`INSERT INTO mentions (message_id, mailbox) VALUES (?, ?)`,
			id,
			v,
		)
		if err != nil {
			return 0, err
		}
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
		`INSERT INTO mailboxes (workspace, name, description, joined_id, cursor, created_at)
		VALUES (?,?,?,COALESCE((SELECT MAX(id) FROM messages WHERE workspace = ?), 0),COALESCE((SELECT MAX(id) FROM messages WHERE workspace = ?), 0),?)
		ON CONFLICT (workspace,name)
		DO NOTHING RETURNING name, description, cursor, joined_id, created_at`,
		ws,
		name,
		desc,
		ws,
		ws,
		s.Now().Unix(),
	).Scan(&mb.Name, &mb.Description, &mb.Cursor, &mb.JoinedID, &mb.CreatedAt)

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
		`SELECT name, description, cursor, joined_id, created_at FROM mailboxes WHERE name = ? AND workspace = ?`,
		name,
		ws,
	).Scan(&mb.Name, &mb.Description, &mb.Cursor, &mb.JoinedID, &mb.CreatedAt)

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

	closed, err := isClosed(ctx, tx, ws, me)
	if err != nil {
		return PullResult{}, err
	}

	out := PullResult{
		Messages: []Message{},
		Closed:   closed,
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
	closed, err := isClosed(ctx, s.db, ws, me)
	if err != nil {
		return false, err
	}

	if closed {
		return true, nil
	}

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
		) OR EXISTS(
				SELECT 1 FROM messages msg
				WHERE msg.workspace = ? AND kind = 'control' AND id > ?  AND sender <> ?
		)`,
		ws,
		mb.Cursor,
		me,
		me,
		ws,
		mb.Cursor,
		me,
	).Scan(&shouldWake); err != nil {
		return false, fmt.Errorf("wakeable should wake: %w", err)
	}

	return shouldWake, nil
}

func (s *Store) CloseWorkspace(ctx context.Context, ws, sender, body string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	id, err := writeMessage(ctx, tx, ws, sender, body, []string{}, KindControl, s.Now().Unix())
	if err != nil {
		return 0, err
	}

	// Upsert closed_id
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO workspaces(name, closed_id) VALUES (?, ?)
		ON CONFLICT (name) DO UPDATE SET closed_id = excluded.closed_id`,
		ws, id,
	)
	if err != nil {
		return 0, err
	}

	if err = tx.Commit(); err != nil {
		return 0, err
	}

	return id, nil
}

func isClosed(ctx context.Context, q querier, ws, me string) (bool, error) {
	mb, err := fetchMailbox(ctx, q, ws, me)
	if err != nil {
		return false, err
	}

	var wsClosedID sql.Null[int64]
	if err := q.QueryRowContext(
		ctx,
		`SELECT closed_id FROM workspaces WHERE name = ?`,
		ws,
	).Scan(&wsClosedID); err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return false, nil
		default:
			return false, err
		}
	}

	if !wsClosedID.Valid {
		return false, nil
	}

	closerIsMe := false
	if err := q.QueryRowContext(
		ctx,
		`SELECT
			CASE
				WHEN m.sender = ? THEN 1
				ELSE 0
			END AS is_me
		FROM messages m WHERE m.id = ?`,
		me,
		wsClosedID.V,
	).Scan(&closerIsMe); err != nil {
		return false, fmt.Errorf("check ws closed: %w", err)
	}

	if closerIsMe || wsClosedID.V <= mb.JoinedID {
		return false, nil
	}

	return true, nil
}

package store_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/eusean-tg/bro/internal/store"
)

// rawDB opens path with database/sql directly, bypassing Store, so a test
// can build or inspect a file the way another build left it.
func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// unversioned writes a database in migration 1's shape with no recorded
// version, as every build before migrations left it: writer and reader in
// alpha, two messages from writer, and reader's cursor past the first. It
// returns the path and the second message's id.
func unversioned(t *testing.T) (path string, second int64) {
	t.Helper()

	schema, err := os.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	path = filepath.Join(t.TempDir(), "bro.db")
	db := rawDB(t, path)

	exec := func(q string, args ...any) sql.Result {
		t.Helper()
		res, err := db.Exec(q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return res
	}
	exec(string(schema))
	exec(`INSERT INTO mailboxes (workspace, name, created_at) VALUES ('alpha', 'writer', 1), ('alpha', 'reader', 1)`)
	first, _ := exec(`INSERT INTO messages (workspace, sender, body, created_at) VALUES ('alpha', 'writer', 'already read', 1)`).LastInsertId()
	second, _ = exec(`INSERT INTO messages (workspace, sender, body, created_at) VALUES ('alpha', 'writer', 'still unread', 1)`).LastInsertId()
	exec(`UPDATE mailboxes SET cursor = ? WHERE name = 'reader'`, first)

	return path, second
}

// openAt opens a Store on an existing database file with the clock pinned.
func openAt(t *testing.T, path string) *store.Store {
	t.Helper()

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	s.Now = func() time.Time { return testClock }
	return s
}

func TestOpenAdoptsAnUnversionedDatabase(t *testing.T) {
	path, second := unversioned(t)
	s := openAt(t, path)

	res, err := s.Pull(t.Context(), "alpha", "reader", store.PullOptions{})
	if err != nil {
		t.Fatalf("Pull(reader): %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != second {
		t.Errorf("reader got %+v, want only message %d: the cursor from before migrations must hold", res.Messages, second)
	}
	if _, err := s.Register(t.Context(), "alpha", "late", ""); err != nil {
		t.Errorf("Register on an adopted database: %v", err)
	}
}

func TestOpenRefusesADatabaseFromANewerBuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bro.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	db := rawDB(t, path)
	if _, err := db.Exec(`PRAGMA user_version = 1000000`); err != nil {
		t.Fatalf("set user_version: %v", err)
	}

	if s, err := store.Open(path); !errors.Is(err, store.ErrSchemaTooNew) {
		if err == nil {
			s.Close()
		}
		t.Fatalf("Open = %v, want ErrSchemaTooNew: this build cannot know what a newer one changed", err)
	}

	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if v != 1000000 {
		t.Errorf("user_version = %d after a refused Open, want it left at 1000000", v)
	}
}

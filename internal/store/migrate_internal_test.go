package store

import (
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// These tests read the embedded migration files and change the loaded
// list, which only a test inside the package can do. No store test runs in
// parallel, so a changed list cannot leak into another test.

// withMigrations replaces the migration list until the test ends.
func withMigrations(t *testing.T, ms ...string) {
	t.Helper()

	orig := migrations
	migrations = ms
	t.Cleanup(func() { migrations = orig })
}

// openClose opens and closes a Store on path, returning Open's error.
func openClose(t *testing.T, path string) error {
	t.Helper()

	s, err := Open(path)
	if err != nil {
		return err
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return nil
}

// inspect reads path's recorded version and whether it has each table.
func inspect(t *testing.T, path string, tables ...string) (version int, has map[string]bool) {
	t.Helper()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	has = map[string]bool{}
	for _, name := range tables {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
			t.Fatalf("look up table %s: %v", name, err)
		}
		has[name] = n > 0
	}
	return version, has
}

// A file's name is its place in the order, and its place is the version
// that records it. A duplicate number, a gap, or a file slipped in among
// shipped ones would change which script counts as migration n, and a
// database that already ran migration n would never run the new one.
func TestMigrationFilesAreNumberedFromOneWithoutGaps(t *testing.T) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	for i, e := range entries {
		if prefix := fmt.Sprintf("%04d_", i+1); !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".sql") {
			t.Errorf("migration file %d is %s, want a name like %sdescription.sql", i+1, e.Name(), prefix)
		}
	}
	if len(entries) != len(migrations) {
		t.Fatalf("%d migration files but %d migrations loaded", len(entries), len(migrations))
	}
	for i, e := range entries {
		b, err := migrationFiles.ReadFile("migrations/" + e.Name())
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", e.Name(), err)
		}
		if migrations[i] != string(b) {
			t.Errorf("migration %d is not the contents of %s: migrations must load in file-name order", i+1, e.Name())
		}
	}
}

func TestOpenRecordsEveryMigrationOnAFreshFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bro.db")

	if err := openClose(t, path); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if v, _ := inspect(t, path); v != len(migrations) {
		t.Errorf("user_version = %d, want %d, one per migration", v, len(migrations))
	}
}

func TestOpenRunsOnlyTheMigrationsAFileLacks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bro.db")
	if err := openClose(t, path); err != nil {
		t.Fatalf("Open: %v", err)
	}

	withMigrations(t, append(slices.Clone(migrations),
		`CREATE TABLE extra (x INTEGER)`,
		`ALTER TABLE extra ADD COLUMN y INTEGER`, // fails if it runs twice
	)...)
	for i := range 2 {
		if err := openClose(t, path); err != nil {
			t.Fatalf("Open %d after adding two migrations: %v", i+1, err)
		}
	}

	v, has := inspect(t, path, "extra")
	if !has["extra"] {
		t.Error("table extra is missing: the new migrations did not run")
	}
	if v != len(migrations) {
		t.Errorf("user_version = %d, want %d", v, len(migrations))
	}
}

func TestAFailedMigrationKeepsTheOnesBeforeItAndNothingOfItself(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bro.db")
	if err := openClose(t, path); err != nil {
		t.Fatalf("Open: %v", err)
	}
	base := slices.Clone(migrations)
	good := `CREATE TABLE good (x INTEGER)` // fails if it runs twice

	withMigrations(t, append(slices.Clone(base), good,
		`CREATE TABLE half (x INTEGER); INSERT INTO nowhere VALUES (1)`,
	)...)
	if err := openClose(t, path); err == nil {
		t.Fatal("Open = nil with a broken migration, want its error")
	}

	v, has := inspect(t, path, "good", "half")
	if !has["good"] {
		t.Error("table good is missing: a migration that succeeded was lost with the one after it")
	}
	if has["half"] {
		t.Error("table half exists: the broken migration was half applied")
	}
	if v != len(base)+1 {
		t.Errorf("user_version = %d, want %d: it counts the migrations that committed", v, len(base)+1)
	}

	// With the broken migration fixed, the next Open carries on from it.
	withMigrations(t, append(slices.Clone(base), good, `CREATE TABLE fixed (x INTEGER)`)...)
	if err := openClose(t, path); err != nil {
		t.Fatalf("Open after fixing the migration = %v; the one before it must not run again", err)
	}
	if _, has := inspect(t, path, "fixed"); !has["fixed"] {
		t.Error("table fixed is missing after the fixed migration ran")
	}
}

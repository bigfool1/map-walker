package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"
)

func TestOpenCreatesDatabaseAndSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "map-walker.db")

	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file missing: %v", err)
	}

	assertTableExists(t, db.DB, "schema_migrations")
	assertTableExists(t, db.DB, "users")
	assertTableExists(t, db.DB, "sessions")
}

func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "map-walker.db")

	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("first open failed: %v", err)
	}
	db.Close()

	db, err = OpenSQLite(path)
	if err != nil {
		t.Fatalf("second open failed: %v", err)
	}
	defer db.Close()

	versions := mustAppliedVersions(t, db.DB)
	if len(versions) != 1 || versions[0] != 1 {
		t.Fatalf("expected migrations [1], got %v", versions)
	}
}

func TestMigrationsApplyInOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ordered.db")

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	defer sqlDB.Close()

	testFS := fstest.MapFS{
		"migrations/001_first.sql":  {Data: []byte(`CREATE TABLE first_marker (id INTEGER PRIMARY KEY);`)},
		"migrations/002_second.sql": {Data: []byte(`CREATE TABLE second_marker (id INTEGER PRIMARY KEY);`)},
	}

	if err := applyMigrations(sqlDB, "sqlite", testFS, "migrations"); err != nil {
		t.Fatalf("apply migrations failed: %v", err)
	}

	assertTableExists(t, sqlDB, "first_marker")
	assertTableExists(t, sqlDB, "second_marker")

	versions := mustAppliedVersions(t, sqlDB)
	if len(versions) != 2 || versions[0] != 1 || versions[1] != 2 {
		t.Fatalf("expected versions [1 2], got %v", versions)
	}
}

func TestMigrationFailureDoesNotRecordVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "failed.db")

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	defer sqlDB.Close()

	testFS := fstest.MapFS{
		"migrations/001_valid.sql":   {Data: []byte(`CREATE TABLE valid_marker (id INTEGER PRIMARY KEY);`)},
		"migrations/002_invalid.sql": {Data: []byte(`CREATE TABLE invalid_marker (id INTEGER PRIMARY KEY INVALID SYNTAX);`)},
	}

	err = applyMigrations(sqlDB, "sqlite", testFS, "migrations")
	if err == nil {
		t.Fatal("expected migration failure")
	}

	assertTableExists(t, sqlDB, "valid_marker")
	assertTableMissing(t, sqlDB, "invalid_marker")

	versions := mustAppliedVersions(t, sqlDB)
	if len(versions) != 1 || versions[0] != 1 {
		t.Fatalf("expected only migration 1 recorded, got %v", versions)
	}
}

func TestOpenCreatesDataDirectory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "data", "map-walker.db")

	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	defer db.Close()

	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		t.Fatalf("data directory not created: %v", err)
	}
}

func assertTableExists(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var name string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
		table,
	).Scan(&name)
	if err != nil {
		t.Fatalf("table %q missing: %v", table, err)
	}
}

func assertTableMissing(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var name string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
		table,
	).Scan(&name)
	if err != sql.ErrNoRows {
		t.Fatalf("table %q should be missing, got %q err=%v", table, name, err)
	}
}

func mustAppliedVersions(t *testing.T, db *sql.DB) []int {
	t.Helper()
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("query applied versions failed: %v", err)
	}
	defer rows.Close()

	versions := []int{}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan version failed: %v", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate versions failed: %v", err)
	}
	return versions
}

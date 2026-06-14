package storage

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

func migrate(db *sql.DB, driver string) error {
	if err := ensureSchemaMigrationsTable(db); err != nil {
		return err
	}
	return applyMigrations(db, driver, embeddedMigrations, "migrations")
}

func applyMigrations(db *sql.DB, driver string, migrationsFS fs.FS, root string) error {
	if err := ensureSchemaMigrationsTable(db); err != nil {
		return err
	}

	pending, err := loadMigrations(migrationsFS, root)
	if err != nil {
		return err
	}

	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	autoPK := autoPKKeyword(driver)
	for _, migration := range pending {
		if applied[migration.version] {
			continue
		}
		sqlText := strings.ReplaceAll(migration.sql, "__PK_AUTO__", autoPK)
		if err := applyMigration(db, migration.version, migration.name, sqlText); err != nil {
			return fmt.Errorf("apply migration %03d_%s: %w", migration.version, migration.name, err)
		}
	}

	return nil
}

func autoPKKeyword(driver string) string {
	if driver == "mysql" {
		return "BIGINT PRIMARY KEY AUTO_INCREMENT"
	}
	// SQLite: INTEGER PRIMARY KEY 自动成为 rowid alias，插入 NULL 时自增
	return "INTEGER PRIMARY KEY"
}

func ensureSchemaMigrationsTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY NOT NULL,
		applied_at TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func loadMigrations(migrationsFS fs.FS, root string) ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, root)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		version, name, ok := parseMigrationFilename(entry.Name())
		if !ok {
			return nil, fmt.Errorf("invalid migration filename: %s", entry.Name())
		}

		data, err := fs.ReadFile(migrationsFS, path.Join(root, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		migrations = append(migrations, migration{
			version: version,
			name:    name,
			sql:     string(data),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	return migrations, nil
}

func parseMigrationFilename(filename string) (int, string, bool) {
	base := strings.TrimSuffix(filename, ".sql")
	parts := strings.SplitN(base, "_", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, "", false
	}

	version, err := strconv.Atoi(parts[0])
	if err != nil || version <= 0 {
		return 0, "", false
	}

	return version, parts[1], true
}

func appliedVersions(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int]bool{}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}

	return applied, nil
}

func applyMigration(db *sql.DB, version int, name, sqlText string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if _, err := tx.Exec(sqlText); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("exec migration sql: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		version,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record migration version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}

	return nil
}

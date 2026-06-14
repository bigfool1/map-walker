package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

const DefaultDBPath = "data/map-walker.db"

type DB struct {
	*sql.DB
	driver string
	dsn    string
}

// Open 打开数据库连接并执行迁移。
// driver: "sqlite" 或 "mysql"
// dsn: SQLite 文件路径 或 MySQL DSN "user:pass@tcp(host:port)/dbname"
func Open(driver, dsn string) (*DB, error) {
	// SQLite 自动创建数据目录
	if driver == "sqlite" {
		if err := os.MkdirAll(filepath.Dir(dsn), 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	// MySQL 允许多语句执行（迁移 SQL 文件包含多条语句）
	if driver == "mysql" && !strings.Contains(dsn, "multiStatements") {
		if strings.Contains(dsn, "?") {
			dsn += "&multiStatements=true"
		} else {
			dsn += "?multiStatements=true"
		}
	}

	sqlDB, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db := &DB{DB: sqlDB, driver: driver, dsn: dsn}
	if err := db.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if err := migrate(sqlDB, driver); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}

	return db, nil
}

func OpenSQLite(path string) (*DB, error) {
	return Open("sqlite", path)
}

func OpenMySQL(dsn string) (*DB, error) {
	return Open("mysql", dsn)
}

func (db *DB) Driver() string {
	return db.driver
}

func (db *DB) DSN() string {
	return db.dsn
}

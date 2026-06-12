package storage

import (
	"database/sql"
	"fmt"
	"time"
)

type Session struct {
	TokenHash string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (db *DB) CreateSession(session Session) error {
	_, err := db.Exec(
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?)`,
		session.TokenHash,
		session.UserID,
		formatTime(session.CreatedAt),
		formatTime(session.ExpiresAt),
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (db *DB) GetSession(tokenHash string) (Session, error) {
	row := db.QueryRow(
		`SELECT token_hash, user_id, created_at, expires_at
		 FROM sessions WHERE token_hash = ?`,
		tokenHash,
	)
	return scanSession(row)
}

func (db *DB) DeleteSession(tokenHash string) error {
	result, err := db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func scanSession(row *sql.Row) (Session, error) {
	var session Session
	var createdAt, expiresAt string
	err := row.Scan(&session.TokenHash, &session.UserID, &createdAt, &expiresAt)
	if err == sql.ErrNoRows {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("scan session: %w", err)
	}
	session.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Session{}, err
	}
	session.ExpiresAt, err = parseTime(expiresAt)
	if err != nil {
		return Session{}, err
	}
	return session, nil
}

package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type User struct {
	ID                  string
	Username            string
	UsernameNormalized  string
	PasswordHash        string
	CreatedAt           time.Time
	LastLat             sql.NullFloat64
	LastLng             sql.NullFloat64
}

func (db *DB) CreateUser(user User) error {
	_, err := db.Exec(
		`INSERT INTO users (id, username, username_normalized, password_hash, created_at, last_lat, last_lng)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		user.ID,
		user.Username,
		user.UsernameNormalized,
		user.PasswordHash,
		formatTime(user.CreatedAt),
		nullFloat(user.LastLat),
		nullFloat(user.LastLng),
	)
	if err != nil && isUniqueViolation(err) {
		return ErrDuplicateUsername
	}
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (db *DB) GetUserByNormalizedUsername(normalized string) (User, error) {
	row := db.QueryRow(
		`SELECT id, username, username_normalized, password_hash, created_at, last_lat, last_lng
		 FROM users WHERE username_normalized = ?`,
		normalized,
	)
	return scanUser(row)
}

func (db *DB) GetUserByID(id string) (User, error) {
	row := db.QueryRow(
		`SELECT id, username, username_normalized, password_hash, created_at, last_lat, last_lng
		 FROM users WHERE id = ?`,
		id,
	)
	return scanUser(row)
}

func (db *DB) SaveUserPosition(userID string, lat, lng float64) error {
	result, err := db.Exec(
		`UPDATE users SET last_lat = ?, last_lng = ? WHERE id = ?`,
		lat,
		lng,
		userID,
	)
	if err != nil {
		return fmt.Errorf("update user position: %w", err)
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

func (db *DB) GetUserPosition(userID string) (lat, lng float64, ok bool, err error) {
	var lastLat, lastLng sql.NullFloat64
	err = db.QueryRow(
		`SELECT last_lat, last_lng FROM users WHERE id = ?`,
		userID,
	).Scan(&lastLat, &lastLng)
	if err == sql.ErrNoRows {
		return 0, 0, false, ErrNotFound
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("query user position: %w", err)
	}
	if !lastLat.Valid || !lastLng.Valid {
		return 0, 0, false, nil
	}
	return lastLat.Float64, lastLng.Float64, true, nil
}

func scanUser(row *sql.Row) (User, error) {
	var user User
	var createdAt string
	var lastLat, lastLng sql.NullFloat64
	err := row.Scan(
		&user.ID,
		&user.Username,
		&user.UsernameNormalized,
		&user.PasswordHash,
		&createdAt,
		&lastLat,
		&lastLng,
	)
	if err == sql.ErrNoRows {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("scan user: %w", err)
	}
	user.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return User{}, err
	}
	user.LastLat = lastLat
	user.LastLng = lastLng
	return user, nil
}

func isUniqueViolation(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", value, err)
	}
	return parsed, nil
}

func nullFloat(value sql.NullFloat64) any {
	if value.Valid {
		return value.Float64
	}
	return nil
}

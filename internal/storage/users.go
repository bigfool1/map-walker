package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

type User struct {
	ID                 int64
	Username           string
	UsernameNormalized string
	PasswordHash       string
	CreatedAt          time.Time
	LastLat            sql.NullFloat64
	LastLng            sql.NullFloat64
	Appearance         Appearance
	CollectibleScore   int64
	IsSynthetic        bool
}

// CreateUser 插入用户并返回数据库自动生成的 ID。
func (db *DB) CreateUser(user User) (int64, error) {
	appearance := appearanceOrDefault(user.Appearance)
	result, err := db.Exec(
		`INSERT INTO users (username, username_normalized, password_hash, created_at, last_lat, last_lng, appearance_color, appearance_shape, collectible_score, is_synthetic)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.Username,
		user.UsernameNormalized,
		user.PasswordHash,
		formatTime(user.CreatedAt),
		nullFloat(user.LastLat),
		nullFloat(user.LastLng),
		appearance.Color,
		appearance.Shape,
		user.CollectibleScore,
		user.IsSynthetic,
	)
	if err != nil && isUniqueViolation(err) {
		return 0, ErrDuplicateUsername
	}
	if err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

func (db *DB) GetUserByNormalizedUsername(normalized string) (User, error) {
	row := db.QueryRow(
		`SELECT id, username, username_normalized, password_hash, created_at, last_lat, last_lng, appearance_color, appearance_shape, collectible_score, is_synthetic
		 FROM users WHERE username_normalized = ?`,
		normalized,
	)
	return scanUser(row)
}

func (db *DB) GetUserByID(id int64) (User, error) {
	row := db.QueryRow(
		`SELECT id, username, username_normalized, password_hash, created_at, last_lat, last_lng, appearance_color, appearance_shape, collectible_score, is_synthetic
		 FROM users WHERE id = ?`,
		id,
	)
	return scanUser(row)
}

func (db *DB) SaveUserAppearance(userID int64, appearance Appearance) error {
	result, err := db.Exec(
		`UPDATE users SET appearance_color = ?, appearance_shape = ? WHERE id = ?`,
		appearance.Color,
		appearance.Shape,
		userID,
	)
	if err != nil {
		return fmt.Errorf("update user appearance: %w", err)
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

func (db *DB) SaveUserPosition(userID int64, lat, lng float64) error {
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

func (db *DB) GetUserPosition(userID int64) (lat, lng float64, ok bool, err error) {
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

func SavedPositionLoader(db *DB) func(userID int64) (lat, lng float64, ok bool) {
	return func(userID int64) (float64, float64, bool) {
		lat, lng, ok, err := db.GetUserPosition(userID)
		if err != nil || !ok {
			return 0, 0, false
		}
		return lat, lng, true
	}
}

type SavedPlayerState struct {
	Username    string
	Lat         float64
	Lng         float64
	HasPosition bool
	Appearance  Appearance
	Score       int64
	IsSynthetic bool
}

func (db *DB) GetUserSavedState(userID int64) (SavedPlayerState, error) {
	user, err := db.GetUserByID(userID)
	if err != nil {
		return SavedPlayerState{}, err
	}

	state := SavedPlayerState{
		Username:    user.Username,
		Appearance:  user.Appearance,
		Score:       user.CollectibleScore,
		IsSynthetic: user.IsSynthetic,
	}
	if user.LastLat.Valid && user.LastLng.Valid {
		state.Lat = user.LastLat.Float64
		state.Lng = user.LastLng.Float64
		state.HasPosition = true
	}
	return state, nil
}

func SavedPlayerLoader(db *DB) func(userID int64) (SavedPlayerState, bool) {
	return func(userID int64) (SavedPlayerState, bool) {
		state, err := db.GetUserSavedState(userID)
		if err != nil {
			return SavedPlayerState{}, false
		}
		return state, true
	}
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
		&user.Appearance.Color,
		&user.Appearance.Shape,
		&user.CollectibleScore,
		&user.IsSynthetic,
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
	// SQLite
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return true
	}
	// MySQL ER_DUP_ENTRY
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return true
	}
	return false
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

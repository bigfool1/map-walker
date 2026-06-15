package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const SyntheticUsernameNormalizedPrefix = "synthetic_"

type SyntheticUserRecord struct {
	UserID      int64
	Username    string
	HasPosition bool
	Lat         float64
	Lng         float64
	Appearance  Appearance
}

type PrepareSyntheticUserParams struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	Appearance   Appearance
	InitialLat   float64
	InitialLng   float64
}

type PrepareSyntheticUserResult struct {
	UserID              int64
	Created             bool
	AppearanceCorrected bool
	PositionInitialized bool
}

func (db *DB) LoadSyntheticUsers() ([]SyntheticUserRecord, error) {
	rows, err := db.Query(
		`SELECT id, username, last_lat, last_lng, appearance_color, appearance_shape
		 FROM users
		 WHERE username_normalized LIKE ?`,
		SyntheticUsernameNormalizedPrefix+"%",
	)
	if err != nil {
		return nil, fmt.Errorf("query synthetic users: %w", err)
	}
	defer rows.Close()

	records := []SyntheticUserRecord{}
	for rows.Next() {
		record, err := scanSyntheticUserRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate synthetic users: %w", err)
	}
	return records, nil
}

func (db *DB) PrepareSyntheticUser(params PrepareSyntheticUserParams) (PrepareSyntheticUserResult, error) {
	normalized := strings.ToLower(params.Username)

	user, err := db.GetUserByNormalizedUsername(normalized)
	if errors.Is(err, ErrNotFound) {
		return db.createSyntheticUser(params, normalized)
	}
	if err != nil {
		return PrepareSyntheticUserResult{}, err
	}

	result := PrepareSyntheticUserResult{UserID: user.ID}

	if user.Appearance != params.Appearance {
		if err := db.SaveUserAppearance(user.ID, params.Appearance); err != nil {
			return PrepareSyntheticUserResult{}, err
		}
		result.AppearanceCorrected = true
	}

	if !user.LastLat.Valid || !user.LastLng.Valid {
		if err := db.SaveUserPosition(user.ID, params.InitialLat, params.InitialLng); err != nil {
			return PrepareSyntheticUserResult{}, err
		}
		result.PositionInitialized = true
	}

	return result, nil
}

func (db *DB) createSyntheticUser(params PrepareSyntheticUserParams, normalized string) (PrepareSyntheticUserResult, error) {
	id, err := db.CreateUser(User{
		Username:           params.Username,
		UsernameNormalized: normalized,
		PasswordHash:       params.PasswordHash,
		CreatedAt:          params.CreatedAt,
		LastLat:            sql.NullFloat64{Valid: true, Float64: params.InitialLat},
		LastLng:            sql.NullFloat64{Valid: true, Float64: params.InitialLng},
		Appearance:         params.Appearance,
	})
	if err != nil {
		return PrepareSyntheticUserResult{}, err
	}

	return PrepareSyntheticUserResult{
		UserID:              id,
		Created:             true,
		PositionInitialized: true,
	}, nil
}

type BulkCreateSyntheticUserParams struct {
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	Appearance   Appearance
	InitialLat   float64
	InitialLng   float64
}

func (db *DB) BulkCreateSyntheticUsers(params []BulkCreateSyntheticUserParams) (int, error) {
	if len(params) == 0 {
		return 0, nil
	}

	const row = "(?, ?, ?, ?, ?, ?, ?, ?)"
	placeholders := make([]string, 0, len(params))
	values := make([]any, 0, len(params)*8)

	for _, p := range params {
		normalized := strings.ToLower(p.Username)
		appearance := appearanceOrDefault(p.Appearance)
		placeholders = append(placeholders, row)
		values = append(values,
			p.Username,
			normalized,
			p.PasswordHash,
			formatTime(p.CreatedAt),
			p.InitialLat,
			p.InitialLng,
			appearance.Color,
			appearance.Shape,
		)
	}

	query := fmt.Sprintf(
		`INSERT INTO users (username, username_normalized, password_hash, created_at, last_lat, last_lng, appearance_color, appearance_shape)
		 VALUES %s`,
		strings.Join(placeholders, ", "),
	)

	_, err := db.Exec(query, values...)
	if err != nil {
		return 0, fmt.Errorf("bulk create synthetic users: %w", err)
	}
	return len(params), nil
}

type BulkPositionEntry struct {
	UserID int64
	Lat    float64
	Lng    float64
}

func (db *DB) BulkUpdateAppearances(userIDs []int64, appearance Appearance) error {
	if len(userIDs) == 0 {
		return nil
	}

	placeholders := make([]string, len(userIDs))
	args := make([]any, 0, len(userIDs)+2)
	args = append(args, appearance.Color, appearance.Shape)
	for i, id := range userIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	query := fmt.Sprintf(
		`UPDATE users SET appearance_color = ?, appearance_shape = ? WHERE id IN (%s)`,
		strings.Join(placeholders, ", "),
	)
	_, err := db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("bulk update appearances: %w", err)
	}
	return nil
}

func (db *DB) BulkInitializePositions(entries []BulkPositionEntry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, entry := range entries {
		_, err := tx.Exec(
			`UPDATE users SET last_lat = ?, last_lng = ?
			 WHERE id = ? AND last_lat IS NULL AND last_lng IS NULL`,
			entry.Lat, entry.Lng, entry.UserID,
		)
		if err != nil {
			return fmt.Errorf("init position user %d: %w", entry.UserID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func scanSyntheticUserRecord(rows *sql.Rows) (SyntheticUserRecord, error) {
	var record SyntheticUserRecord
	var lastLat, lastLng sql.NullFloat64
	if err := rows.Scan(
		&record.UserID,
		&record.Username,
		&lastLat,
		&lastLng,
		&record.Appearance.Color,
		&record.Appearance.Shape,
	); err != nil {
		return SyntheticUserRecord{}, fmt.Errorf("scan synthetic user: %w", err)
	}
	if lastLat.Valid && lastLng.Valid {
		record.HasPosition = true
		record.Lat = lastLat.Float64
		record.Lng = lastLng.Float64
	}
	return record, nil
}

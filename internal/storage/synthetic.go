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
	IsSynthetic bool
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
		`SELECT id, username, last_lat, last_lng, appearance_color, appearance_shape, is_synthetic
		 FROM users
		 WHERE is_synthetic = TRUE`,
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

	if !user.IsSynthetic {
		if _, err := db.Exec(
			`UPDATE users SET is_synthetic = TRUE WHERE id = ?`,
			user.ID,
		); err != nil {
			return PrepareSyntheticUserResult{}, fmt.Errorf("correct synthetic marker: %w", err)
		}
	}

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
		IsSynthetic:        true,
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

// 每批最多 500 行，确保 SQLite/MySQL 参数数在 SQLITE_MAX_VARIABLE_NUMBER 内
const bulkCreateChunkSize = 500
const bulkCreateColumns = 10

// CorrectSyntheticMarkers 修正已有合成账户的 is_synthetic 标记
func (db *DB) CorrectSyntheticMarkers() (int64, error) {
	result, err := db.Exec(
		`UPDATE users SET is_synthetic = TRUE
		 WHERE username_normalized LIKE ? AND is_synthetic = FALSE`,
		SyntheticUsernameNormalizedPrefix+"%",
	)
	if err != nil {
		return 0, fmt.Errorf("correct synthetic markers: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

func (db *DB) BulkCreateSyntheticUsers(params []BulkCreateSyntheticUserParams) (int, error) {
	if len(params) == 0 {
		return 0, nil
	}

	// 预展开行模板，避免每块重复分配
	rowPlaceholders := make([]string, bulkCreateChunkSize)
	for i := range rowPlaceholders {
		rowPlaceholders[i] = "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	}

	total := 0
	for start := 0; start < len(params); start += bulkCreateChunkSize {
		end := start + bulkCreateChunkSize
		if end > len(params) {
			end = len(params)
		}
		chunk := params[start:end]

		values := make([]any, 0, len(chunk)*bulkCreateColumns)
		for _, p := range chunk {
			normalized := strings.ToLower(p.Username)
			appearance := appearanceOrDefault(p.Appearance)
			values = append(values,
				p.Username,
				normalized,
				p.PasswordHash,
				formatTime(p.CreatedAt),
				p.InitialLat,
				p.InitialLng,
				appearance.Color,
				appearance.Shape,
				int64(0), // collectible_score
				true,     // is_synthetic
			)
		}

		query := fmt.Sprintf(
			`INSERT INTO users (username, username_normalized, password_hash, created_at, last_lat, last_lng, appearance_color, appearance_shape, collectible_score, is_synthetic)
			 VALUES %s`,
			strings.Join(rowPlaceholders[:len(chunk)], ", "),
		)

		if _, err := db.Exec(query, values...); err != nil {
			return total, fmt.Errorf("bulk create synthetic users: %w", err)
		}
		total += len(chunk)
	}
	return total, nil
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

	for start := 0; start < len(userIDs); start += bulkCreateChunkSize {
		end := start + bulkCreateChunkSize
		if end > len(userIDs) {
			end = len(userIDs)
		}
		chunk := userIDs[start:end]

		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)+2)
		args = append(args, appearance.Color, appearance.Shape)
		for i, id := range chunk {
			placeholders[i] = "?"
			args = append(args, id)
		}

		query := fmt.Sprintf(
			`UPDATE users SET appearance_color = ?, appearance_shape = ? WHERE id IN (%s)`,
			strings.Join(placeholders, ", "),
		)
		if _, err := db.Exec(query, args...); err != nil {
			return fmt.Errorf("bulk update appearances: %w", err)
		}
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
		&record.IsSynthetic,
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

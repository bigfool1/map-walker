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
	UserID      string
	Username    string
	HasPosition bool
	Lat         float64
	Lng         float64
	Appearance  Appearance
}

type PrepareSyntheticUserParams struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	Appearance   Appearance
	InitialLat   float64
	InitialLng   float64
}

type PrepareSyntheticUserResult struct {
	UserID              string
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
	err := db.CreateUser(User{
		ID:                 params.ID,
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
		UserID:              params.ID,
		Created:             true,
		PositionInitialized: true,
	}, nil
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

package storage

import (
	"testing"
	"time"
)

var syntheticAppearance = Appearance{Color: "#ff8c00", Shape: "diamond"}

func TestLoadSyntheticUsersReturnsAllPrefixMatches(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	users := []User{
		{ID: "syn-1", Username: "synthetic_1", UsernameNormalized: "synthetic_1", PasswordHash: "hash-1", CreatedAt: now},
		{ID: "syn-2", Username: "synthetic_2", UsernameNormalized: "synthetic_2", PasswordHash: "hash-2", CreatedAt: now},
		{ID: "syn-bad", Username: "synthetic_foo", UsernameNormalized: "synthetic_foo", PasswordHash: "hash-bad", CreatedAt: now},
		{ID: "real-1", Username: "alice", UsernameNormalized: "alice", PasswordHash: "hash-alice", CreatedAt: now},
	}
	for _, user := range users {
		if err := db.CreateUser(user); err != nil {
			t.Fatalf("create user %q failed: %v", user.Username, err)
		}
	}

	records, err := db.LoadSyntheticUsers()
	if err != nil {
		t.Fatalf("LoadSyntheticUsers failed: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 synthetic prefix matches, got %d", len(records))
	}

	byID := map[string]SyntheticUserRecord{}
	for _, record := range records {
		byID[record.UserID] = record
	}
	if byID["syn-bad"].Username != "synthetic_foo" {
		t.Fatalf("expected strict filtering left to caller, got %+v", byID["syn-bad"])
	}
}

func TestPrepareSyntheticUserCreatesMissingAccount(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	result, err := db.PrepareSyntheticUser(PrepareSyntheticUserParams{
		ID:           "syn-1",
		Username:     "synthetic_1",
		PasswordHash: "hash-1",
		CreatedAt:    now,
		Appearance:   syntheticAppearance,
		InitialLat:   31.23,
		InitialLng:   121.47,
	})
	if err != nil {
		t.Fatalf("PrepareSyntheticUser failed: %v", err)
	}
	if !result.Created || !result.PositionInitialized || result.AppearanceCorrected {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.UserID != "syn-1" {
		t.Fatalf("unexpected user id: %q", result.UserID)
	}

	user, err := db.GetUserByID("syn-1")
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	if user.PasswordHash != "hash-1" {
		t.Fatalf("unexpected password hash: %q", user.PasswordHash)
	}
	if user.Appearance != syntheticAppearance {
		t.Fatalf("unexpected appearance: %+v", user.Appearance)
	}
	lat, lng, ok, err := db.GetUserPosition("syn-1")
	if err != nil || !ok || lat != 31.23 || lng != 121.47 {
		t.Fatalf("unexpected position: ok=%v lat=%v lng=%v err=%v", ok, lat, lng, err)
	}
}

func TestPrepareSyntheticUserPreservesPasswordAndPosition(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	if err := db.CreateUser(User{
		ID:                 "syn-1",
		Username:           "synthetic_1",
		UsernameNormalized: "synthetic_1",
		PasswordHash:       "original-hash",
		CreatedAt:          now,
		Appearance:         Appearance{Color: "#3388ff", Shape: "circle"},
	}); err != nil {
		t.Fatalf("create user failed: %v", err)
	}
	if err := db.SaveUserPosition("syn-1", 31.1, 121.1); err != nil {
		t.Fatalf("save position failed: %v", err)
	}

	result, err := db.PrepareSyntheticUser(PrepareSyntheticUserParams{
		ID:           "ignored-id",
		Username:     "synthetic_1",
		PasswordHash: "replacement-hash",
		CreatedAt:    now,
		Appearance:   syntheticAppearance,
		InitialLat:   99.9,
		InitialLng:   88.8,
	})
	if err != nil {
		t.Fatalf("PrepareSyntheticUser failed: %v", err)
	}
	if result.Created || result.PositionInitialized {
		t.Fatalf("expected no create or position init, got %+v", result)
	}
	if !result.AppearanceCorrected {
		t.Fatalf("expected appearance correction")
	}

	user, err := db.GetUserByID("syn-1")
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	if user.PasswordHash != "original-hash" {
		t.Fatalf("password hash changed to %q", user.PasswordHash)
	}
	lat, lng, ok, err := db.GetUserPosition("syn-1")
	if err != nil || !ok || lat != 31.1 || lng != 121.1 {
		t.Fatalf("position overwritten: ok=%v lat=%v lng=%v err=%v", ok, lat, lng, err)
	}
}

func TestPrepareSyntheticUserInitializesAbsentPosition(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	if err := db.CreateUser(User{
		ID:                 "syn-1",
		Username:           "synthetic_1",
		UsernameNormalized: "synthetic_1",
		PasswordHash:       "hash-1",
		CreatedAt:          now,
		Appearance:         syntheticAppearance,
	}); err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	result, err := db.PrepareSyntheticUser(PrepareSyntheticUserParams{
		ID:           "ignored-id",
		Username:     "synthetic_1",
		PasswordHash: "ignored-hash",
		CreatedAt:    now,
		Appearance:   syntheticAppearance,
		InitialLat:   31.5,
		InitialLng:   121.5,
	})
	if err != nil {
		t.Fatalf("PrepareSyntheticUser failed: %v", err)
	}
	if result.Created || result.AppearanceCorrected || !result.PositionInitialized {
		t.Fatalf("unexpected result: %+v", result)
	}

	lat, lng, ok, err := db.GetUserPosition("syn-1")
	if err != nil || !ok || lat != 31.5 || lng != 121.5 {
		t.Fatalf("unexpected position: ok=%v lat=%v lng=%v err=%v", ok, lat, lng, err)
	}
}

func TestPrepareSyntheticUserIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	params := PrepareSyntheticUserParams{
		ID:           "syn-1",
		Username:     "synthetic_1",
		PasswordHash: "hash-1",
		CreatedAt:    now,
		Appearance:   syntheticAppearance,
		InitialLat:   31.23,
		InitialLng:   121.47,
	}

	first, err := db.PrepareSyntheticUser(params)
	if err != nil {
		t.Fatalf("first prepare failed: %v", err)
	}
	if !first.Created || !first.PositionInitialized {
		t.Fatalf("expected first prepare to create account, got %+v", first)
	}
	second, err := db.PrepareSyntheticUser(params)
	if err != nil {
		t.Fatalf("second prepare failed: %v", err)
	}
	if second.Created || second.AppearanceCorrected || second.PositionInitialized {
		t.Fatalf("expected idempotent second prepare, got %+v", second)
	}

	userAfterFirst, err := db.GetUserByID("syn-1")
	if err != nil {
		t.Fatalf("get user after first prepare failed: %v", err)
	}
	userAfterSecond, err := db.GetUserByID("syn-1")
	if err != nil {
		t.Fatalf("get user after second prepare failed: %v", err)
	}
	if userAfterFirst != userAfterSecond {
		t.Fatalf("stored state changed on repeat prepare: first=%+v second=%+v", userAfterFirst, userAfterSecond)
	}
}

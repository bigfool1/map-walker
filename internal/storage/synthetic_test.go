package storage

import (
	"testing"
	"time"
)

var syntheticAppearance = Appearance{Color: "#ff8c00", Shape: "diamond"}

func TestLoadSyntheticUsersReturnsAllPrefixMatches(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	// 创建 3 个 synthetic_ 用户 + 1 个普通用户
	type testUser struct {
		id       int64
		username string
	}
	var synUsers []testUser
	for _, name := range []string{"synthetic_1", "synthetic_2", "synthetic_foo"} {
		id, err := db.CreateUser(User{
			Username:           name,
			UsernameNormalized: name,
			PasswordHash:       "hash",
			CreatedAt:          now,
			IsSynthetic:        true,
		})
		if err != nil {
			t.Fatalf("create user %q failed: %v", name, err)
		}
		synUsers = append(synUsers, testUser{id: id, username: name})
	}

	_, err := db.CreateUser(User{
		Username:           "alice",
		UsernameNormalized: "alice",
		PasswordHash:       "hash-alice",
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("create alice failed: %v", err)
	}

	records, err := db.LoadSyntheticUsers()
	if err != nil {
		t.Fatalf("LoadSyntheticUsers failed: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 synthetic prefix matches, got %d", len(records))
	}

	byID := map[int64]SyntheticUserRecord{}
	for _, record := range records {
		byID[record.UserID] = record
	}
	synBadID := synUsers[2].id
	if byID[synBadID].Username != "synthetic_foo" {
		t.Fatalf("expected strict filtering left to caller, got %+v", byID[synBadID])
	}
}

func TestPrepareSyntheticUserCreatesMissingAccount(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	result, err := db.PrepareSyntheticUser(PrepareSyntheticUserParams{
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

	user, err := db.GetUserByID(result.UserID)
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	if user.PasswordHash != "hash-1" {
		t.Fatalf("unexpected password hash: %q", user.PasswordHash)
	}
	if user.Appearance != syntheticAppearance {
		t.Fatalf("unexpected appearance: %+v", user.Appearance)
	}
	lat, lng, ok, err := db.GetUserPosition(result.UserID)
	if err != nil || !ok || lat != 31.23 || lng != 121.47 {
		t.Fatalf("unexpected position: ok=%v lat=%v lng=%v err=%v", ok, lat, lng, err)
	}
}

func TestPrepareSyntheticUserPreservesPasswordAndPosition(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	id, err := db.CreateUser(User{
		Username:           "synthetic_1",
		UsernameNormalized: "synthetic_1",
		PasswordHash:       "original-hash",
		CreatedAt:          now,
		Appearance:         Appearance{Color: "#3388ff", Shape: "circle"},
	})
	if err != nil {
		t.Fatalf("create user failed: %v", err)
	}
	if err := db.SaveUserPosition(id, 31.1, 121.1); err != nil {
		t.Fatalf("save position failed: %v", err)
	}

	result, err := db.PrepareSyntheticUser(PrepareSyntheticUserParams{
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

	user, err := db.GetUserByID(id)
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	if user.PasswordHash != "original-hash" {
		t.Fatalf("password hash changed to %q", user.PasswordHash)
	}
	lat, lng, ok, err := db.GetUserPosition(id)
	if err != nil || !ok || lat != 31.1 || lng != 121.1 {
		t.Fatalf("position overwritten: ok=%v lat=%v lng=%v err=%v", ok, lat, lng, err)
	}
}

func TestPrepareSyntheticUserInitializesAbsentPosition(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	id, err := db.CreateUser(User{
		Username:           "synthetic_1",
		UsernameNormalized: "synthetic_1",
		PasswordHash:       "hash-1",
		CreatedAt:          now,
		Appearance:         syntheticAppearance,
	})
	if err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	result, err := db.PrepareSyntheticUser(PrepareSyntheticUserParams{
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

	lat, lng, ok, err := db.GetUserPosition(id)
	if err != nil || !ok || lat != 31.5 || lng != 121.5 {
		t.Fatalf("unexpected position: ok=%v lat=%v lng=%v err=%v", ok, lat, lng, err)
	}
}

func TestPrepareSyntheticUserIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	params := PrepareSyntheticUserParams{
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

	userAfterFirst, err := db.GetUserByID(first.UserID)
	if err != nil {
		t.Fatalf("get user after first prepare failed: %v", err)
	}
	userAfterSecond, err := db.GetUserByID(first.UserID)
	if err != nil {
		t.Fatalf("get user after second prepare failed: %v", err)
	}
	if userAfterFirst != userAfterSecond {
		t.Fatalf("stored state changed on repeat prepare: first=%+v second=%+v", userAfterFirst, userAfterSecond)
	}
}

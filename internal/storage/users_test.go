package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestCreateUserAndLookupByNormalizedUsername(t *testing.T) {
	db := openTestDB(t)

	err := db.CreateUser(User{
		ID:                 "user-1",
		Username:           "Alice",
		UsernameNormalized: "alice",
		PasswordHash:       "hash",
		CreatedAt:          time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	user, err := db.GetUserByNormalizedUsername("alice")
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if user.Username != "Alice" || user.UsernameNormalized != "alice" {
		t.Fatalf("unexpected user: %+v", user)
	}
}

func TestCreateUserRejectsDuplicateNormalizedUsername(t *testing.T) {
	db := openTestDB(t)

	first := User{
		ID:                 "user-1",
		Username:           "Alice",
		UsernameNormalized: "alice",
		PasswordHash:       "hash-1",
		CreatedAt:          time.Now().UTC(),
	}
	second := User{
		ID:                 "user-2",
		Username:           "ALICE",
		UsernameNormalized: "alice",
		PasswordHash:       "hash-2",
		CreatedAt:          time.Now().UTC(),
	}

	if err := db.CreateUser(first); err != nil {
		t.Fatalf("create first user failed: %v", err)
	}
	if err := db.CreateUser(second); err != ErrDuplicateUsername {
		t.Fatalf("expected duplicate username, got %v", err)
	}
}

func TestSaveAndGetUserPosition(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateUser(User{
		ID:                 "user-1",
		Username:           "alice",
		UsernameNormalized: "alice",
		PasswordHash:       "hash",
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	if _, _, ok, err := db.GetUserPosition("user-1"); err != nil || ok {
		t.Fatalf("expected no saved position, ok=%v err=%v", ok, err)
	}

	if err := db.SaveUserPosition("user-1", 31.23, 121.47); err != nil {
		t.Fatalf("save position failed: %v", err)
	}

	lat, lng, ok, err := db.GetUserPosition("user-1")
	if err != nil || !ok {
		t.Fatalf("get position failed: ok=%v err=%v", ok, err)
	}
	if lat != 31.23 || lng != 121.47 {
		t.Fatalf("unexpected position: %v %v", lat, lng)
	}
}

func TestSavedPlayerLoader(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateUser(User{
		ID:                 "user-1",
		Username:           "alice",
		UsernameNormalized: "alice",
		PasswordHash:       "hash",
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create user failed: %v", err)
	}
	if err := db.SaveUserAppearance("user-1", Appearance{Color: "#ff6600", Shape: "triangle"}); err != nil {
		t.Fatalf("save appearance failed: %v", err)
	}
	if err := db.SaveUserPosition("user-1", 31.5, 121.5); err != nil {
		t.Fatalf("save position failed: %v", err)
	}

	loader := SavedPlayerLoader(db)
	state, ok := loader("user-1")
	if !ok {
		t.Fatal("expected saved player state")
	}
	if !state.HasPosition || state.Lat != 31.5 || state.Lng != 121.5 {
		t.Fatalf("unexpected position: %+v", state)
	}
	if state.Appearance.Color != "#ff6600" || state.Appearance.Shape != "triangle" {
		t.Fatalf("unexpected appearance: %+v", state.Appearance)
	}

	if _, ok := loader("missing-user"); ok {
		t.Fatal("expected missing user to have no saved state")
	}
}

func TestSavedPlayerLoaderWithoutSavedPosition(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateUser(User{
		ID:                 "user-1",
		Username:           "alice",
		UsernameNormalized: "alice",
		PasswordHash:       "hash",
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create user failed: %v", err)
	}
	if err := db.SaveUserAppearance("user-1", Appearance{Color: "#ff6600", Shape: "diamond"}); err != nil {
		t.Fatalf("save appearance failed: %v", err)
	}

	state, ok := SavedPlayerLoader(db)("user-1")
	if !ok {
		t.Fatal("expected saved player state")
	}
	if state.HasPosition {
		t.Fatalf("expected no saved position: %+v", state)
	}
	if state.Appearance.Color != "#ff6600" || state.Appearance.Shape != "diamond" {
		t.Fatalf("unexpected appearance: %+v", state.Appearance)
	}
}

func TestSavedPositionLoader(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateUser(User{
		ID:                 "user-1",
		Username:           "alice",
		UsernameNormalized: "alice",
		PasswordHash:       "hash",
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create user failed: %v", err)
	}
	if err := db.SaveUserPosition("user-1", 31.5, 121.5); err != nil {
		t.Fatalf("save position failed: %v", err)
	}

	loader := SavedPositionLoader(db)
	lat, lng, ok := loader("user-1")
	if !ok {
		t.Fatal("expected saved position")
	}
	if lat != 31.5 || lng != 121.5 {
		t.Fatalf("unexpected loaded position: %v %v", lat, lng)
	}

	if _, _, ok := loader("missing-user"); ok {
		t.Fatal("expected missing user to have no saved position")
	}
}

func TestNewUserHasDefaultAppearance(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateUser(User{
		ID:                 "user-1",
		Username:           "alice",
		UsernameNormalized: "alice",
		PasswordHash:       "hash",
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	user, err := db.GetUserByID("user-1")
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	if user.Appearance.Color != DefaultAppearanceColor || user.Appearance.Shape != DefaultAppearanceShape {
		t.Fatalf("unexpected default appearance: %+v", user.Appearance)
	}
}

func TestSaveAndReloadUserAppearance(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateUser(User{
		ID:                 "user-1",
		Username:           "alice",
		UsernameNormalized: "alice",
		PasswordHash:       "hash",
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	custom := Appearance{Color: "#ff6600", Shape: "diamond"}
	if err := db.SaveUserAppearance("user-1", custom); err != nil {
		t.Fatalf("save appearance failed: %v", err)
	}

	user, err := db.GetUserByID("user-1")
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	if user.Appearance != custom {
		t.Fatalf("unexpected appearance: %+v", user.Appearance)
	}
}

func TestSaveUserAppearanceMissingUser(t *testing.T) {
	db := openTestDB(t)

	err := db.SaveUserAppearance("missing-user", Appearance{
		Color: "#ff6600",
		Shape: "diamond",
	})
	if err != ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestSessionCreateGetDelete(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateUser(User{
		ID:                 "user-1",
		Username:           "alice",
		UsernameNormalized: "alice",
		PasswordHash:       "hash",
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	now := time.Now().UTC()
	session := Session{
		TokenHash: "token-hash",
		UserID:    "user-1",
		CreatedAt: now,
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	if err := db.CreateSession(session); err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	got, err := db.GetSession("token-hash")
	if err != nil {
		t.Fatalf("get session failed: %v", err)
	}
	if got.UserID != "user-1" {
		t.Fatalf("unexpected session: %+v", got)
	}

	if err := db.DeleteSession("token-hash"); err != nil {
		t.Fatalf("delete session failed: %v", err)
	}
	if _, err := db.GetSession("token-hash"); err != ErrNotFound {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

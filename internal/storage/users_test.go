package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
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

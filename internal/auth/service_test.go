package auth

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"map-walker/internal/storage"
)

func openTestService(t *testing.T) *Service {
	t.Helper()
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewService(db)
}

func TestNormalizeUsername(t *testing.T) {
	if got := NormalizeUsername("Alice"); got != "alice" {
		t.Fatalf("expected alice, got %q", got)
	}
}

func TestValidateUsername(t *testing.T) {
	if err := ValidateUsername("ab"); err != ErrInvalidUsername {
		t.Fatalf("expected invalid username, got %v", err)
	}
	if err := ValidateUsername("abc"); err != nil {
		t.Fatalf("expected valid username, got %v", err)
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("short"); err != ErrInvalidPassword {
		t.Fatalf("expected invalid password, got %v", err)
	}
	if err := ValidatePassword("long-enough"); err != nil {
		t.Fatalf("expected valid password, got %v", err)
	}
}

func TestHashSessionTokenIsDeterministic(t *testing.T) {
	first := HashSessionToken("session-token")
	second := HashSessionToken("session-token")
	if first == "" || first != second {
		t.Fatalf("expected stable hash, got %q and %q", first, second)
	}
}

func TestRegisterLoginAndAuthenticate(t *testing.T) {
	svc := openTestService(t)

	token, user, err := svc.Register("Alice", "password123")
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if user.Username != "Alice" {
		t.Fatalf("unexpected user: %+v", user)
	}
	if user.Appearance.Color != storage.DefaultAppearanceColor || user.Appearance.Shape != storage.DefaultAppearanceShape {
		t.Fatalf("unexpected default appearance: %+v", user.Appearance)
	}

	_, _, err = svc.Register("alice", "another-password")
	if err != ErrUsernameUnavailable {
		t.Fatalf("expected duplicate username, got %v", err)
	}

	loginToken, loginUser, err := svc.Login("alice", "password123")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if loginUser.ID != user.ID {
		t.Fatalf("expected same user id, got %v", loginUser)
	}

	authenticated, err := svc.AuthenticateSession(loginToken)
	if err != nil {
		t.Fatalf("authenticate failed: %v", err)
	}
	if authenticated.Username != "Alice" {
		t.Fatalf("unexpected authenticated user: %+v", authenticated)
	}
	if authenticated.Appearance.Color != storage.DefaultAppearanceColor || authenticated.Appearance.Shape != storage.DefaultAppearanceShape {
		t.Fatalf("unexpected authenticated appearance: %+v", authenticated.Appearance)
	}

	if err := svc.Logout(token); err != nil {
		t.Fatalf("logout failed: %v", err)
	}
	if _, err := svc.AuthenticateSession(token); err != ErrUnauthenticated {
		t.Fatalf("expected unauthenticated after logout, got %v", err)
	}
}

func TestAuthenticateReturnsSavedAppearance(t *testing.T) {
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewService(db)

	_, user, err := svc.Register("Bob", "password123")
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}

	custom := storage.Appearance{Color: "#ff6600", Shape: "triangle"}
	if err := db.SaveUserAppearance(user.ID, custom); err != nil {
		t.Fatalf("save appearance failed: %v", err)
	}

	token, loginUser, err := svc.Login("Bob", "password123")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if loginUser.Appearance != custom {
		t.Fatalf("login appearance = %+v, want %+v", loginUser.Appearance, custom)
	}

	authenticated, err := svc.AuthenticateSession(token)
	if err != nil {
		t.Fatalf("authenticate failed: %v", err)
	}
	if authenticated.Appearance != custom {
		t.Fatalf("authenticated appearance = %+v, want %+v", authenticated.Appearance, custom)
	}
}

func TestLoginRejectsInvalidCredentials(t *testing.T) {
	svc := openTestService(t)

	if _, _, err := svc.Register("Alice", "password123"); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if _, _, err := svc.Login("Alice", "wrong-password"); err != ErrInvalidCredentials {
		t.Fatalf("expected invalid credentials, got %v", err)
	}
}

func TestAuthenticateRejectsExpiredSession(t *testing.T) {
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	svc := NewService(db)
	svc.now = func() time.Time { return now }

	if err := db.CreateUser(storage.User{
		ID:                 "user-1",
		Username:           "Alice",
		UsernameNormalized: "alice",
		PasswordHash:       "hash",
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("create user failed: %v", err)
	}

	token := "session-token"
	if err := db.CreateSession(storage.Session{
		TokenHash: HashSessionToken(token),
		UserID:    "user-1",
		CreatedAt: now.Add(-31 * 24 * time.Hour),
		ExpiresAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	if _, err := svc.AuthenticateSession(token); err != ErrSessionExpired {
		t.Fatalf("expected expired session, got %v", err)
	}
}

func TestSessionCookiePolicy(t *testing.T) {
	cookie := NewSessionCookie("token-value", true)
	if cookie.Name != CookieName || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected secure cookie: %+v", cookie)
	}
	if cookie.MaxAge != int(SessionDuration/time.Second) {
		t.Fatalf("unexpected max age: %d", cookie.MaxAge)
	}

	cleared := ClearSessionCookie(false)
	if cleared.MaxAge != -1 || cleared.Value != "" {
		t.Fatalf("unexpected cleared cookie: %+v", cleared)
	}
}

func TestCheckPasswordUsesBcryptHash(t *testing.T) {
	hash, err := HashPassword("password123")
	if err != nil {
		t.Fatalf("hash password failed: %v", err)
	}
	if hash == "password123" || !CheckPassword(hash, "password123") {
		t.Fatal("expected bcrypt hash verification to succeed")
	}
	if CheckPassword(hash, "wrong-password") {
		t.Fatal("expected bcrypt hash verification to fail")
	}
}

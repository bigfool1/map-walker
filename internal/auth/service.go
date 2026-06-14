package auth

import (
	"errors"
	"time"

	"map-walker/internal/storage"
)

type Service struct {
	db  *storage.DB
	now func() time.Time
}

type User struct {
	ID         int64
	Username   string
	Appearance storage.Appearance
}

func NewService(db *storage.DB) *Service {
	return &Service{
		db:  db,
		now: time.Now,
	}
}

func (s *Service) Register(username, password string) (sessionToken string, user User, err error) {
	if err := ValidateRegistrationUsername(username); err != nil {
		return "", User{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return "", User{}, err
	}

	passwordHash, err := HashPassword(password)
	if err != nil {
		return "", User{}, err
	}

	userID, err := s.db.CreateUser(storage.User{
		Username:           username,
		UsernameNormalized: NormalizeUsername(username),
		PasswordHash:       passwordHash,
		CreatedAt:          s.now(),
	})
	if errors.Is(err, storage.ErrDuplicateUsername) {
		return "", User{}, ErrUsernameUnavailable
	}
	if err != nil {
		return "", User{}, err
	}

	token, err := s.createSession(userID)
	if err != nil {
		return "", User{}, err
	}

	record, err := s.db.GetUserByID(userID)
	if err != nil {
		return "", User{}, err
	}

	return token, authUserFromRecord(record), nil
}

func (s *Service) Login(username, password string) (sessionToken string, user User, err error) {
	if err := ValidateUsername(username); err != nil {
		return "", User{}, ErrInvalidCredentials
	}
	if err := ValidatePassword(password); err != nil {
		return "", User{}, ErrInvalidCredentials
	}

	record, err := s.db.GetUserByNormalizedUsername(NormalizeUsername(username))
	if errors.Is(err, storage.ErrNotFound) {
		return "", User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", User{}, err
	}
	if !CheckPassword(record.PasswordHash, password) {
		return "", User{}, ErrInvalidCredentials
	}

	token, err := s.createSession(record.ID)
	if err != nil {
		return "", User{}, err
	}

	return token, authUserFromRecord(record), nil
}

func (s *Service) Logout(sessionToken string) error {
	if sessionToken == "" {
		return ErrUnauthenticated
	}
	err := s.db.DeleteSession(HashSessionToken(sessionToken))
	if errors.Is(err, storage.ErrNotFound) {
		return ErrUnauthenticated
	}
	return err
}

func (s *Service) AuthenticateSession(sessionToken string) (User, error) {
	if sessionToken == "" {
		return User{}, ErrUnauthenticated
	}

	session, err := s.db.GetSession(HashSessionToken(sessionToken))
	if errors.Is(err, storage.ErrNotFound) {
		return User{}, ErrUnauthenticated
	}
	if err != nil {
		return User{}, err
	}
	if !session.ExpiresAt.After(s.now()) {
		return User{}, ErrSessionExpired
	}

	record, err := s.db.GetUserByID(session.UserID)
	if errors.Is(err, storage.ErrNotFound) {
		return User{}, ErrUnauthenticated
	}
	if err != nil {
		return User{}, err
	}

	return authUserFromRecord(record), nil
}

func (s *Service) SaveAppearance(userID int64, appearance storage.Appearance) error {
	return s.db.SaveUserAppearance(userID, appearance)
}

func authUserFromRecord(record storage.User) User {
	return User{
		ID:         record.ID,
		Username:   record.Username,
		Appearance: record.Appearance,
	}
}

func (s *Service) createSession(userID int64) (string, error) {
	token, err := NewSessionToken()
	if err != nil {
		return "", err
	}

	now := s.now()
	err = s.db.CreateSession(storage.Session{
		TokenHash: HashSessionToken(token),
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: SessionExpiresAt(now),
	})
	if err != nil {
		return "", err
	}

	return token, nil
}

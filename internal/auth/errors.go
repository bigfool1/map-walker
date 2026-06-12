package auth

import "errors"

var (
	ErrInvalidUsername     = errors.New("invalid username")
	ErrInvalidPassword     = errors.New("invalid password")
	ErrUsernameUnavailable = errors.New("username unavailable")
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrUnauthenticated     = errors.New("unauthenticated")
	ErrSessionExpired      = errors.New("session expired")
	ErrInvalidAppearance   = errors.New("invalid appearance")
)

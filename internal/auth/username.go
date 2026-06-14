package auth

import (
	"strings"
	"unicode/utf8"

	"map-walker/internal/synthetic"
)

const (
	minUsernameLength = 3
	maxUsernameLength = 32
)

func NormalizeUsername(username string) string {
	return strings.ToLower(username)
}

func ValidateUsername(username string) error {
	length := utf8.RuneCountInString(username)
	if length < minUsernameLength || length > maxUsernameLength {
		return ErrInvalidUsername
	}
	return nil
}

func ValidateRegistrationUsername(username string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if synthetic.HasReservedPrefix(username) {
		return ErrUsernameUnavailable
	}
	return nil
}

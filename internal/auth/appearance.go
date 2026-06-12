package auth

import (
	"regexp"
	"strings"

	"map-walker/internal/storage"
)

var appearanceColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

var allowedAppearanceShapes = map[string]struct{}{
	"circle":   {},
	"square":   {},
	"diamond":  {},
	"triangle": {},
}

func ValidateAppearance(color, shape string) (storage.Appearance, error) {
	if color == "" || shape == "" {
		return storage.Appearance{}, ErrInvalidAppearance
	}
	if !appearanceColorPattern.MatchString(color) {
		return storage.Appearance{}, ErrInvalidAppearance
	}
	if _, ok := allowedAppearanceShapes[shape]; !ok {
		return storage.Appearance{}, ErrInvalidAppearance
	}
	return storage.Appearance{
		Color: strings.ToLower(color),
		Shape: shape,
	}, nil
}

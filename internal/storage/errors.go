package storage

import "errors"

var (
	ErrDuplicateUsername = errors.New("duplicate username")
	ErrNotFound          = errors.New("not found")
)

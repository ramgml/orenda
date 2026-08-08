// Package user holds the User domain entity.
//
// The package is deliberately tiny: the entity is a plain Go struct with no
// SQL/JSON tags (those are presentation concerns handled by the storage and
// API layers). The repository contract lives in repository.go in the same
// package.
package user

import (
	"errors"
	"time"
)

// Role enumerates known user roles.
//
// Phase 0/1 only ship the single-owner case; the enum exists so the storage
// layer can store "owner" verbatim and a future multi-role migration can
// extend without changing call sites.
type Role string

const (
	RoleOwner Role = "owner"
)

// Sentinel errors returned by User repository implementations.
//
// Handlers translate these into HTTP status codes; CLI tools translate them
// into human-readable messages.
var (
	ErrNotFound     = errors.New("user: not found")
	ErrEmailTaken   = errors.New("user: email already in use")
	ErrInvalidInput = errors.New("user: invalid input")
)

// User is the canonical user entity.
//
// Timestamps are kept as time.Time here and converted to ISO-8601 UTC strings
// at the storage boundary. The domain layer does not assume any particular
// wire format.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"` // never serialise
	DisplayName  string    `json:"display_name"`
	Role         Role      `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Validate returns an error if the User fields are inconsistent.
//
// Called by repositories before INSERT/UPDATE and by handlers when accepting
// input from the wire. The repository is allowed to reject invalid data even
// when the caller hasn't validated first — defence in depth.
func (u *User) Validate() error {
	if u.Email == "" {
		return ErrInvalidInput
	}
	if u.PasswordHash == "" {
		return ErrInvalidInput
	}
	if u.DisplayName == "" {
		return ErrInvalidInput
	}
	if u.Role == "" {
		u.Role = RoleOwner // default
	}
	return nil
}

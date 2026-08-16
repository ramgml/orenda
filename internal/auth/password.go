// Package auth provides password hashing, JWT session tokens and opaque API
// tokens used by Orenda.
//
// Three layers:
//
//   - Password: bcrypt(cost=12) for human user accounts. The hash is stored
//     in users.password_hash.
//
//   - JWT: HS256-signed token carried in the orenda_session cookie. Used by
//     the SPA so the browser handles the cookie transparently.
//
//   - API token: opaque 32-byte random string, bcrypt-hashed and stored in
//     api_tokens.hash. Carried in the Authorization: Bearer header by agents
//     and CLI tools.
package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidPassword is returned when Verify rejects a plaintext password.
var ErrInvalidPassword = errors.New("auth: invalid password")

// HashPassword returns a bcrypt hash of the plaintext at the requested cost.
//
// Cost < 4 is clamped to 4 (bcrypt minimum); cost > 31 is rejected because
// bcrypt truncates the cost to 5 bits.
func HashPassword(plain string, cost int) (string, error) {
	if plain == "" {
		return "", errors.New("auth: empty password")
	}
	if cost < bcrypt.MinCost {
		cost = bcrypt.DefaultCost
	}
	if cost > bcrypt.MaxCost {
		return "", fmt.Errorf("auth: bcrypt cost %d exceeds max %d", cost, bcrypt.MaxCost)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), cost)
	if err != nil {
		return "", fmt.Errorf("auth: bcrypt: %w", err)
	}
	return string(h), nil
}

// VerifyPassword reports whether plain matches the stored bcrypt hash.
//
// nil error means success. ErrInvalidPassword is returned for any mismatch,
// malformed hash, or low-level bcrypt failure so callers can use a single
// error path (and avoid leaking details about which condition failed).
func VerifyPassword(hash, plain string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
	if err != nil {
		return ErrInvalidPassword
	}
	return nil
}

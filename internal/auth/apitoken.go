package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidTokenFormat is returned when the plaintext token doesn't match
// the expected encoding (32 random bytes, base64url without padding).
var ErrInvalidTokenFormat = errors.New("auth: invalid api token format")

// apiTokenBytes is the number of random bytes used to mint an API token.
// 32 bytes = 256 bits of entropy, base64url-encoded into 43 chars.
const apiTokenBytes = 32

// NewAPIToken generates a fresh opaque API token and returns its plaintext.
//
// The plaintext is shown to the caller exactly once (typically when the
// token is created); only the bcrypt hash is stored in api_tokens.hash.
func NewAPIToken() (string, error) {
	buf := make([]byte, apiTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashAPIToken bcrypts the plaintext for storage.
//
// Same cost model as HashPassword.
func HashAPIToken(plain string, cost int) (string, error) {
	return HashPassword(plain, cost) // same algorithm, different semantics
}

// VerifyAPIToken reports whether the plaintext matches the stored hash.
//
// Use this when looking up the api_tokens row by hash; the returned sentinel
// is the same as for passwords so middleware can collapse both cases.
func VerifyAPIToken(hash, plain string) error {
	return VerifyPassword(hash, plain) // intentional alias — bcrypt is bcrypt
}

// NormalizeAPIToken trims whitespace; API tokens are case-sensitive but the
// Authorization header is sometimes mangled by clients.
func NormalizeAPIToken(plain string) string {
	return strings.TrimSpace(plain)
}

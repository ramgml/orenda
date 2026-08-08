package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken is returned for any parse/verify failure so callers can
// treat all JWT problems with a single error path.
var ErrInvalidToken = errors.New("auth: invalid token")

// Claims is the JWT body carried in the orenda_session cookie.
//
// Subject holds the user ID; custom fields are stored in the registered
// claims (DisplayName, Role).
type Claims struct {
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	jwt.RegisteredClaims
}

// Signer issues and verifies JWTs with HS256.
//
// The secret is shared between Issue and Verify — a different secret makes
// every previously-issued token invalid, which is the intended behaviour for
// a forced logout / key rotation.
type Signer struct {
	secret []byte
	ttl    time.Duration
	issuer string
}

// NewSigner constructs a Signer with the given secret, token TTL, and issuer.
//
// An empty secret panics — the operator must provide ORENDA_AUTH__JWT_SECRET.
func NewSigner(secret string, ttl time.Duration, issuer string) *Signer {
	if secret == "" {
		panic("auth: NewSigner: empty secret (set ORENDA_AUTH__JWT_SECRET)")
	}
	return &Signer{
		secret: []byte(secret),
		ttl:    ttl,
		issuer: issuer,
	}
}

// Issue creates a signed JWT for the given user.
//
// The token's `sub` claim holds the user id; `iat`/`exp` are derived from
// the signer's configured TTL.
func (s *Signer) Issue(userID, displayName, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		DisplayName: displayName,
		Role:        role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("auth: sign jwt: %w", err)
	}
	return signed, nil
}

// Verify parses and validates a JWT, returning the claims on success.
//
// All parse / signature / expiry failures collapse to ErrInvalidToken so
// middleware can produce a single 401 response.
func (s *Signer) Verify(raw string) (*Claims, error) {
	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected method %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil || !tok.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

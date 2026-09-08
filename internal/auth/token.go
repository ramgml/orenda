package auth

import "time"

// TokenRow is the storage-layer projection of api_tokens used by auth
// middleware (and by the repo that backs it).
//
// It lives in the auth package so both internal/api (which only needs to
// reference the fields) and internal/storage/sqlite (which reads/writes the
// table) can depend on it without an import cycle.
type TokenRow struct {
	ID         string
	UserID     string
	Name       string
	Hash       string
	ScopesJSON string
	// ExpiresAt is the credential deadline (api_tokens.expires_at).
	// nil means the token never expires. T182: the auth middleware
	// treats a row whose deadline has passed as if the token did not
	// exist — expired ≡ not found on the wire.
	ExpiresAt *time.Time
}

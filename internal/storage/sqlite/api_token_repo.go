// Package sqlite — API token repository implementation.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/auth"
)

// StoredToken is one row in the api_tokens table, decoded into a struct.
//
// It extends auth.TokenRow with storage-only fields (LastUsedAt, ExpiresAt,
// CreatedAt). The auth layer only needs the public subset.
type StoredToken struct {
	auth.TokenRow
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	CreatedAt  time.Time
}

// apiTokenRepo persists API tokens in api_tokens.
type apiTokenRepo struct {
	db *sql.DB
}

// NewAPITokenRepository returns a usable repo for Phase 1 auth middleware.
func NewAPITokenRepository(db *sql.DB) *apiTokenRepo {
	return &apiTokenRepo{db: db}
}

// Create inserts a fresh token. The hash must already be bcrypt'd by the
// caller (see auth.HashAPIToken). The returned StoredToken has timestamps set.
func (r *apiTokenRepo) Create(ctx context.Context, userID, name, hash, scopesJSON string, expiresAt *time.Time) (*StoredToken, error) {
	if userID == "" || hash == "" {
		return nil, errors.New("apiTokenRepo.Create: userID and hash required")
	}
	id := newUUID()
	const q = `
		INSERT INTO api_tokens (id, user_id, name, hash, scopes, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
	`
	var exp sql.NullString
	if expiresAt != nil {
		exp = sql.NullString{String: formatTime(*expiresAt), Valid: true}
	}
	if _, err := r.db.ExecContext(ctx, q, id, userID, name, hash, scopesJSON, exp); err != nil {
		return nil, fmt.Errorf("apiToken.Create: %w", err)
	}
	return r.GetByID(ctx, id)
}

// GetByID returns the token by primary key.
func (r *apiTokenRepo) GetByID(ctx context.Context, id string) (*StoredToken, error) {
	const q = `
		SELECT id, user_id, name, hash, scopes, last_used_at, expires_at, created_at
		FROM api_tokens WHERE id = ?
	`
	row := r.db.QueryRowContext(ctx, q, id)
	var (
		t       StoredToken
		lastUse sql.NullString
		exp     sql.NullString
		cAt     string
	)
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Hash, &t.ScopesJSON, &lastUse, &exp, &cAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("apiToken.GetByID: %w", err)
	}
	if lastUse.Valid {
		tt := parseTime(lastUse.String)
		t.LastUsedAt = &tt
	}
	if exp.Valid {
		tt := parseTime(exp.String)
		t.ExpiresAt = &tt
	}
	t.CreatedAt = parseTime(cAt)
	return &t, nil
}

// ListAllHashes returns every token keyed by hash; used by auth middleware to
// find the row matching an incoming Authorization: Bearer header.
func (r *apiTokenRepo) ListAllHashes(ctx context.Context) (map[string]auth.TokenRow, error) {
	const q = `SELECT id, user_id, name, hash, scopes, last_used_at, expires_at, created_at FROM api_tokens`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("apiToken.ListAllHashes: %w", err)
	}
	defer rows.Close()
	out := make(map[string]auth.TokenRow)
	for rows.Next() {
		var (
			t       StoredToken
			lastUse sql.NullString
			exp     sql.NullString
			cAt     string
		)
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Hash, &t.ScopesJSON, &lastUse, &exp, &cAt); err != nil {
			return nil, err
		}
		if lastUse.Valid {
			tt := parseTime(lastUse.String)
			t.LastUsedAt = &tt
		}
		if exp.Valid {
			tt := parseTime(exp.String)
			t.ExpiresAt = &tt
		}
		t.CreatedAt = parseTime(cAt)
		out[t.Hash] = t.TokenRow
	}
	return out, rows.Err()
}

// TouchLastUsed updates last_used_at; called by auth middleware on each
// successful API token verification. Throttled by the caller to avoid
// hammering the DB on every request — Phase 9 will add a smarter strategy.
func (r *apiTokenRepo) TouchLastUsed(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at = datetime('now') WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("apiToken.TouchLastUsed: %w", err)
	}
	return nil
}

// ErrTokenNotFound is returned by GetByID when no row matches.
var ErrTokenNotFound = errors.New("apiToken: not found")

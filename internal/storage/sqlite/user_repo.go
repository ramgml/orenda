// Package sqlite — User repository implementation.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ramgml/orenda/internal/domain/user"
)

// userRepo persists users in the SQLite users table.
type userRepo struct {
	db *sql.DB
}

// UserRepo is the exported alias so callers that need the concrete type
// (e.g. for FirstID) can name it.
type UserRepo = userRepo

// NewUserRepository returns a user.Repository backed by db.
func NewUserRepository(db *sql.DB) *userRepo {
	return &userRepo{db: db}
}

func (r *userRepo) Create(ctx context.Context, u *user.User) error {
	if err := u.Validate(); err != nil {
		return err
	}
	if u.ID == "" {
		u.ID = newUUID()
	}

	const q = `
		INSERT INTO users (id, email, password_hash, display_name, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, datetime('now'), datetime('now'))
	`
	_, err := r.db.ExecContext(ctx, q,
		u.ID, strings.ToLower(u.Email), u.PasswordHash, u.DisplayName, string(u.Role),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return user.ErrEmailTaken
		}
		return fmt.Errorf("user.Create: %w", err)
	}

	// Re-read to populate CreatedAt/UpdatedAt into u.
	got, err := r.GetByID(ctx, u.ID)
	if err != nil {
		return err
	}
	*u = *got
	return nil
}

func (r *userRepo) GetByID(ctx context.Context, id string) (*user.User, error) {
	const q = `
		SELECT id, email, password_hash, display_name, role, created_at, updated_at
		FROM users WHERE id = ?
	`
	return scanUser(r.db.QueryRowContext(ctx, q, id))
}

func (r *userRepo) GetByEmail(ctx context.Context, email string) (*user.User, error) {
	const q = `
		SELECT id, email, password_hash, display_name, role, created_at, updated_at
		FROM users WHERE email = ?
	`
	return scanUser(r.db.QueryRowContext(ctx, q, strings.ToLower(email)))
}

func (r *userRepo) Update(ctx context.Context, u *user.User) error {
	if err := u.Validate(); err != nil {
		return err
	}
	const q = `
		UPDATE users
		SET email = ?, password_hash = ?, display_name = ?, role = ?
		WHERE id = ?
	`
	res, err := r.db.ExecContext(ctx, q,
		strings.ToLower(u.Email), u.PasswordHash, u.DisplayName, string(u.Role), u.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return user.ErrEmailTaken
		}
		return fmt.Errorf("user.Update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("user.Update: rows: %w", err)
	}
	if n == 0 {
		return user.ErrNotFound
	}
	got, err := r.GetByID(ctx, u.ID)
	if err != nil {
		return err
	}
	*u = *got
	return nil
}

func (r *userRepo) Delete(ctx context.Context, id string) error {
	const q = `DELETE FROM users WHERE id = ?`
	res, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("user.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("user.Delete: rows: %w", err)
	}
	if n == 0 {
		return user.ErrNotFound
	}
	return nil
}

// List returns all users ordered by created_at ASC. Used by the CLI
// `orenda user list` command. Returns an empty (non-nil) slice when
// the table has no rows.
func (r *userRepo) List(ctx context.Context) ([]*user.User, error) {
	const q = `
		SELECT id, email, password_hash, display_name, role, created_at, updated_at
		FROM users ORDER BY created_at ASC, id ASC
	`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("user.List: %w", err)
	}
	defer rows.Close()

	users := make([]*user.User, 0)
	for rows.Next() {
		var (
			u    user.User
			role string
			cAt  string
			uAt  string
		)
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.DisplayName, &role, &cAt, &uAt); err != nil {
			return nil, fmt.Errorf("user.List: scan: %w", err)
		}
		u.Role = user.Role(role)
		u.CreatedAt = parseTime(cAt)
		u.UpdatedAt = parseTime(uAt)
		users = append(users, &u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("user.List: rows: %w", err)
	}
	return users, nil
}

// FirstID returns the id of the first user row (single-owner model).
// Used by the bot callback resolver which needs the owner's id.
func (r *userRepo) FirstID(ctx context.Context) (string, error) {
	var id string
	err := r.db.QueryRowContext(ctx,
		`SELECT id FROM users ORDER BY created_at ASC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", user.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("user.FirstID: %w", err)
	}
	return id, nil
}

// scanUser reads one row into a User; ErrNoRows becomes user.ErrNotFound.
func scanUser(row *sql.Row) (*user.User, error) {
	var (
		u    user.User
		role string
		cAt  string
		uAt  string
	)
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.DisplayName, &role, &cAt, &uAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, user.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("user.Scan: %w", err)
	}
	u.Role = user.Role(role)
	u.CreatedAt = parseTime(cAt)
	u.UpdatedAt = parseTime(uAt)
	return &u, nil
}

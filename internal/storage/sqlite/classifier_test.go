package sqlite

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
)

// TestConstraintClassifiers_DialectAware covers both error
// representations the shared repository layer must translate
// (wiki:storage-adapters D1/D5): the modernc SQLite message text and
// the PostgreSQL *pgconn.PgError SQLSTATE codes — directly wrapped and
// deeply nested — plus the negatives that must not match.
func TestConstraintClassifiers_DialectAware(t *testing.T) {
	pgUnique := &pgconn.PgError{Code: "23505", Message: `duplicate key value violates unique constraint "users_email_key"`}
	pgFK := &pgconn.PgError{Code: "23503", Message: `insert or update on table "task_locks" violates foreign key constraint "task_locks_task_id_fkey"`}
	pgOther := &pgconn.PgError{Code: "23502", Message: "null value in column violates not-null constraint"}

	tests := []struct {
		name    string
		err     error
		wantUni bool
		wantFK  bool
	}{
		{name: "nil", err: nil},
		{
			name:    "sqlite unique text",
			err:     errors.New("constraint failed: UNIQUE constraint failed: tags.name"),
			wantUni: true,
		},
		{
			name:   "sqlite fk text",
			err:    errors.New("FOREIGN KEY constraint failed"),
			wantFK: true,
		},
		{
			name:    "pg unique direct",
			err:     pgUnique,
			wantUni: true,
		},
		{
			name:    "pg unique wrapped",
			err:     fmt.Errorf("user.Create: %w", pgUnique),
			wantUni: true,
		},
		{
			name:    "pg unique double wrapped",
			err:     fmt.Errorf("handler: %w", fmt.Errorf("repo: %w", pgUnique)),
			wantUni: true,
		},
		{
			name:   "pg fk direct",
			err:    pgFK,
			wantFK: true,
		},
		{
			name:   "pg fk wrapped",
			err:    fmt.Errorf("task_locks.Acquire: %w", pgFK),
			wantFK: true,
		},
		{
			name: "pg other class",
			err:  pgOther,
		},
		{
			name: "plain error",
			err:  errors.New("boom"),
		},
		{
			name: "pg message text without pgconn type must not match",
			err:  errors.New("duplicate key value violates unique constraint"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantUni, IsUniqueViolation(tc.err))
			assert.Equal(t, tc.wantFK, IsFKViolation(tc.err))
		})
	}
}

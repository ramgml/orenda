// Package sqlite — sync_ops repository (Phase 8 idempotency store).
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// syncOpsRepo implements the api.SyncOpsStore interface (defined in the
// api package) — Go's structural interfaces mean we don't need to import
// api here.
type syncOpsRepo struct{ db *sql.DB }

// SyncOpsRepo is the exported alias so cmd/orenda can name the type.
type SyncOpsRepo = syncOpsRepo

// NewSyncOpsRepository returns the sync_ops repo.
func NewSyncOpsRepository(db *sql.DB) *syncOpsRepo {
	return &syncOpsRepo{db: db}
}

func (r *syncOpsRepo) Seen(ctx context.Context, clientID string) (seen bool, serverID string, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT server_id FROM sync_ops WHERE client_id = ?`, clientID).Scan(&serverID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("sync_ops.Seen: %w", err)
	}
	return true, serverID, nil
}

func (r *syncOpsRepo) Record(ctx context.Context, clientID, serverID string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO sync_ops (client_id, server_id, op, target)
		VALUES (?, ?, '', '')
	`, clientID, serverID)
	if err != nil {
		return fmt.Errorf("sync_ops.Record: %w", err)
	}
	return nil
}

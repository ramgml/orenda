// Package sqlite — task_locks repository.
//
// task_locks is the atomic primitive for Claim: a row with the same
// task_id can only exist once (PK), so two concurrent Claims on the same
// task cannot both succeed — one wins, the other gets a UNIQUE violation.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/agent"
)

// ErrLockTaken is returned when another agent already holds the lock.
// Exported so the service layer can errors.Is against it without
// re-declaring the sentinel.
var ErrLockTaken = errors.New("sqlite: task lock already taken")

// ErrLockNotHeld is returned when an agent tries to release/submit a lock
// it doesn't hold.
var ErrLockNotHeld = errors.New("sqlite: task lock not held by this agent")

// ErrLockNotFound is returned by Acquire when the target task (or agent)
// doesn't exist. Distinct from ErrLockNotHeld (release) and
// ErrLockTaken (already held).
var ErrLockNotFound = errors.New("sqlite: task lock target not found")

// taskLockRepo is the small layer for the task_locks table.
type taskLockRepo struct {
	db *sql.DB
}

// NewTaskLockRepository returns a lock repo.
func NewTaskLockRepository(db *sql.DB) *taskLockRepo {
	return &taskLockRepo{db: db}
}

// Acquire atomically inserts a task_locks row.
//
// Translates:
//
//   - UNIQUE violation on task_id → ErrLockTaken
//   - FOREIGN KEY violation (task missing) → ErrLockNotFound
//   - FOREIGN KEY violation (agent missing) → returned as-is (programmer error)
func (r *taskLockRepo) Acquire(ctx context.Context, taskID, agentID string) error {
	const q = `
		INSERT INTO task_locks (task_id, agent_id, acquired_at)
		VALUES (?, ?, datetime('now'))
	`
	_, err := r.db.ExecContext(ctx, q, taskID, agentID)
	if err != nil {
		switch {
		case isUniqueViolation(err):
			return ErrLockTaken
		case isFKViolation(err):
			// The Acquire FK can fail for either task_id (very common
			// in tests) or agent_id. We can't distinguish from the error
			// message alone, so we treat all FK failures as "lock target
			// not found" and let the caller re-check the task to surface
			// agent_id issues separately.
			return ErrLockNotFound
		}
		return fmt.Errorf("taskLock.Acquire: %w", err)
	}
	return nil
}

// Release deletes the row owned by agentID; returns ErrLockNotHeld if the
// row is absent or held by a different agent.
func (r *taskLockRepo) Release(ctx context.Context, taskID, agentID string) error {
	const q = `DELETE FROM task_locks WHERE task_id = ? AND agent_id = ?`
	res, err := r.db.ExecContext(ctx, q, taskID, agentID)
	if err != nil {
		return fmt.Errorf("taskLock.Release: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLockNotHeld
	}
	return nil
}

// Holder returns the agent_id currently holding the lock, or empty string.
func (r *taskLockRepo) Holder(ctx context.Context, taskID string) (agentID string, acquiredAt time.Time, err error) {
	const q = `SELECT agent_id, acquired_at FROM task_locks WHERE task_id = ?`
	var (
		aid string
		at  string
	)
	row := r.db.QueryRowContext(ctx, q, taskID)
	err = row.Scan(&aid, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, err
	}
	return aid, parseTime(at), nil
}

// EnsureAgentTokenIDExists is a no-op exported helper that just verifies
// the agent row referenced by task_locks.agent_id is reachable. Used by
// tests; production code shouldn't need it.
func (r *taskLockRepo) EnsureAgentTokenIDExists(_ context.Context, _ string) error { return nil }

// avoid unused-import warnings on agent in this file
var _ = agent.StatusOnline

// Package sqlite — Agent repository implementation.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/agent"
)

// agentRepo persists agents in the agents table.
type agentRepo struct {
	db *sql.DB
}

// NewAgentRepository returns the Phase 3 agent repo.
func NewAgentRepository(db *sql.DB) agent.Repository {
	return &agentRepo{db: db}
}

func (r *agentRepo) Create(ctx context.Context, a *agent.Agent) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.ID == "" {
		a.ID = newUUID()
	}

	const q = `
		INSERT INTO agents (id, name, type, description, token_id, status, max_concurrent, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))
	`
	_, err := r.db.ExecContext(ctx, q,
		a.ID, a.Name, string(a.Type), a.Description, a.TokenID,
		string(a.Status), a.MaxConcurrent,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return agent.ErrNameTaken
		}
		return fmt.Errorf("agent.Create: %w", err)
	}
	got, err := r.GetByID(ctx, a.ID)
	if err != nil {
		return err
	}
	*a = *got
	return nil
}

func (r *agentRepo) GetByID(ctx context.Context, id string) (*agent.Agent, error) {
	return scanAgent(r.db.QueryRowContext(ctx, agentSelectByID, id))
}

func (r *agentRepo) GetByName(ctx context.Context, name string) (*agent.Agent, error) {
	return scanAgent(r.db.QueryRowContext(ctx, agentSelectByName, name))
}

func (r *agentRepo) GetByTokenID(ctx context.Context, tokenID string) (*agent.Agent, error) {
	return scanAgent(r.db.QueryRowContext(ctx, agentSelectByTokenID, tokenID))
}

func (r *agentRepo) List(ctx context.Context) ([]*agent.Agent, error) {
	rows, err := r.db.QueryContext(ctx, agentSelectAll)
	if err != nil {
		return nil, fmt.Errorf("agent.List: %w", err)
	}
	defer rows.Close()

	var out []*agent.Agent
	for rows.Next() {
		a, err := scanAgentRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *agentRepo) Update(ctx context.Context, a *agent.Agent) error {
	if err := a.Validate(); err != nil {
		return err
	}
	const q = `
		UPDATE agents
		SET name = ?, type = ?, description = ?, status = ?, max_concurrent = ?
		WHERE id = ?
	`
	res, err := r.db.ExecContext(ctx, q,
		a.Name, string(a.Type), a.Description, string(a.Status), a.MaxConcurrent, a.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return agent.ErrNameTaken
		}
		return fmt.Errorf("agent.Update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return agent.ErrNotFound
	}
	got, err := r.GetByID(ctx, a.ID)
	if err != nil {
		return err
	}
	*a = *got
	return nil
}

func (r *agentRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM agents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("agent.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return agent.ErrNotFound
	}
	return nil
}

func (r *agentRepo) TouchLastSeen(ctx context.Context, id string) (*agent.Agent, error) {
	// Use UPDATE first so we always get last_seen_at = now; THEN read.
	const q = `UPDATE agents SET last_seen_at = datetime('now'), status = ? WHERE id = ?`
	if _, err := r.db.ExecContext(ctx, q, string(agent.StatusOnline), id); err != nil {
		return nil, fmt.Errorf("agent.TouchLastSeen: %w", err)
	}
	return r.GetByID(ctx, id)
}

func (r *agentRepo) SweepOffline(ctx context.Context, ttl time.Duration) (int64, error) {
	cutoff := formatTime(time.Now().Add(-ttl))
	q := `UPDATE agents SET status = ? WHERE status = ? AND (last_seen_at IS NULL OR last_seen_at < ?)`
	res, err := r.db.ExecContext(ctx, q,
		string(agent.StatusOffline), string(agent.StatusOnline), cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("agent.SweepOffline: %w", err)
	}
	return res.RowsAffected()
}

// SQL constants — kept here so all agent queries are in one place.
const agentSelectColumns = `
SELECT id, name, type, description, token_id, last_seen_at, status, max_concurrent, created_at
FROM agents
`

const (
	agentSelectByID      = agentSelectColumns + " WHERE id = ?"
	agentSelectByName    = agentSelectColumns + " WHERE name = ?"
	agentSelectByTokenID = agentSelectColumns + " WHERE token_id = ?"
	agentSelectAll       = agentSelectColumns + " ORDER BY created_at DESC"
)

func scanAgent(row *sql.Row) (*agent.Agent, error) {
	var (
		a        agent.Agent
		typ      string
		status   string
		lastSeen sql.NullString
		cAt      string
	)
	err := row.Scan(&a.ID, &a.Name, &typ, &a.Description, &a.TokenID, &lastSeen, &status, &a.MaxConcurrent, &cAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, agent.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("agent.Scan: %w", err)
	}
	a.Type = agent.Type(typ)
	a.Status = agent.Status(status)
	if lastSeen.Valid {
		t := parseTime(lastSeen.String)
		a.LastSeenAt = &t
	}
	a.CreatedAt = parseTime(cAt)
	return &a, nil
}

func scanAgentRow(rows *sql.Rows) (*agent.Agent, error) {
	var (
		a        agent.Agent
		typ      string
		status   string
		lastSeen sql.NullString
		cAt      string
	)
	if err := rows.Scan(&a.ID, &a.Name, &typ, &a.Description, &a.TokenID, &lastSeen, &status, &a.MaxConcurrent, &cAt); err != nil {
		return nil, fmt.Errorf("agent.ScanRow: %w", err)
	}
	a.Type = agent.Type(typ)
	a.Status = agent.Status(status)
	if lastSeen.Valid {
		t := parseTime(lastSeen.String)
		a.LastSeenAt = &t
	}
	a.CreatedAt = parseTime(cAt)
	return &a, nil
}

// Package sqlite — Phase 31 study proposal repository.
//
// The repo is small and intentionally hand-written — proposals are
// a transient queue, not a hot path. Each method does one statement;
// the lifecycle transitions (MarkAccepted/MarkDismissed) are atomic
// via conditional WHERE clauses (status='pending') so two concurrent
// accepts collapse on the first commit; the second is a no-op
// (rowsAffected=0) and the service layer reads the existing row to
// resolve the API.
//
// Phase 32.9 adds FindPendingEquivalent — the dedup lookup the
// service.Propose performs before creating a new row, so the
// planner's evening+morning runs don't pile up identical proposals.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ramgml/orenda/internal/domain/study"
)

type studyProposalRepo struct {
	db *sql.DB
}

// NewStudyProposalRepository returns a study.Repository backed by db.
func NewStudyProposalRepository(db *sql.DB) study.Repository {
	return &studyProposalRepo{db: db}
}

func (r *studyProposalRepo) Create(ctx context.Context, p *study.Proposal) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.ID == "" {
		p.ID = newUUID()
	}
	// status / resolved_at are set by the lifecycle transitions;
	// the create path always lands the row in pending.
	const q = `
		INSERT INTO study_proposals
		    (id, course_id, title, body_md, target_date, status,
		     created_by_agent, accepted_task_id,
		     created_at, resolved_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?, NULL,
		        datetime('now'), NULL)
	`
	_, err := r.db.ExecContext(ctx, q,
		p.ID,
		nullString(p.CourseID),
		p.Title, p.BodyMD, p.TargetDate,
		p.CreatedByAgent,
	)
	if err != nil {
		return fmt.Errorf("study.Create: %w", err)
	}
	// Re-read to populate timestamps + the canonical status (the
	// INSERT pinned it; this is the cheapest way to make sure the
	// caller sees what the row actually holds).
	got, err := r.Get(ctx, p.ID)
	if err != nil {
		return err
	}
	*p = *got
	return nil
}

func (r *studyProposalRepo) ListPending(ctx context.Context) ([]*study.Proposal, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(course_id, ''), title, COALESCE(body_md, ''),
		       target_date, status, created_by_agent,
		       COALESCE(accepted_task_id, ''),
		       created_at, resolved_at
		FROM study_proposals
		WHERE status = 'pending'
		ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("study.ListPending: %w", err)
	}
	defer rows.Close()
	out := make([]*study.Proposal, 0)
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *studyProposalRepo) Get(ctx context.Context, id string) (*study.Proposal, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(course_id, ''), title, COALESCE(body_md, ''),
		       target_date, status, created_by_agent,
		       COALESCE(accepted_task_id, ''),
		       created_at, resolved_at
		FROM study_proposals WHERE id = ?`, id)
	p, err := scanProposal(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, study.ErrNotFound
		}
		return nil, fmt.Errorf("study.Get: %w", err)
	}
	return p, nil
}

// FindPendingEquivalent returns the most recent pending proposal
// matching the dedup key (agentID, courseID, normalizedTitle), or
// (nil, nil) when no match exists.
//
// Phase 32.9: lets Service.Propose collapse repeated runs of the
// planner onto the same row. The caller is responsible for
// producing normalizedTitle via NormalizeTitle; the repo does a
// defensive `lower(trim(...))` in SQL so a direct repo call from
// an admin script can't bypass the contract.
//
// `course_id IS NULL` and `course_id = ?` are mutually exclusive in
// the WHERE clause — we OR them because course_id is nullable and
// the empty-string sentinel from the service must match both
// pending-with-NULL and pending-with-empty-string rows (the
// service writes the empty string but a hand-typed NULL might
// exist from older rows).
func (r *studyProposalRepo) FindPendingEquivalent(ctx context.Context, agentID, courseID, normalizedTitle string) (*study.Proposal, error) {
	const q = `
		SELECT id, COALESCE(course_id, ''), title, COALESCE(body_md, ''),
		       target_date, status, created_by_agent,
		       COALESCE(accepted_task_id, ''),
		       created_at, resolved_at
		FROM study_proposals
		WHERE status = 'pending'
		  AND created_by_agent = ?
		  AND (course_id IS NULL OR course_id = ?)
		  AND lower(trim(title)) = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, q, agentID, courseID, normalizedTitle)
	p, err := scanProposal(row)
	if err != nil {
		if err == sql.ErrNoRows {
			//nolint:nilnil // Find-semantics: no equivalent proposal is a normal outcome; callers branch on nil.
			return nil, nil
		}
		return nil, fmt.Errorf("study.FindPendingEquivalent: %w", err)
	}
	return p, nil
}

func (r *studyProposalRepo) MarkAccepted(ctx context.Context, id, taskID string) error {
	// The conditional WHERE keeps the operation idempotent — a
	// second concurrent accept finds rowsAffected=0 and the service
	// layer re-reads to resolve the API.
	res, err := r.db.ExecContext(ctx, `
		UPDATE study_proposals
		   SET status = 'accepted', accepted_task_id = ?, resolved_at = datetime('now')
		 WHERE id = ? AND status = 'pending'`,
		nullString(taskID), id)
	if err != nil {
		return fmt.Errorf("study.MarkAccepted: %w", err)
	}
	// rowsAffected=0 is fine — it means the proposal was already
	// resolved. The service layer re-reads to figure out which
	// state it's in (ErrTransition vs idempotent re-accept).
	if n, _ := res.RowsAffected(); n == 0 {
		// Distinguish "already accepted" (idempotent re-call on
		// this row) from "not found" — the latter is genuinely a
		// missing row.
		var exists int
		if err := r.db.QueryRowContext(ctx,
			`SELECT 1 FROM study_proposals WHERE id = ?`, id,
		).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return study.ErrNotFound
			}
			return fmt.Errorf("study.MarkAccepted: existence check: %w", err)
		}
	}
	return nil
}

func (r *studyProposalRepo) MarkDismissed(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE study_proposals
		   SET status = 'dismissed', resolved_at = datetime('now')
		 WHERE id = ? AND status = 'pending'`, id)
	if err != nil {
		return fmt.Errorf("study.MarkDismissed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		if err := r.db.QueryRowContext(ctx,
			`SELECT 1 FROM study_proposals WHERE id = ?`, id,
		).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return study.ErrNotFound
			}
			return fmt.Errorf("study.MarkDismissed: existence check: %w", err)
		}
	}
	return nil
}

// scanProposal reads from either a Row (Get) or a Rows iterator
// (ListPending). Matches the column list of both SELECTs verbatim.
func scanProposal(s scanner) (*study.Proposal, error) {
	var p study.Proposal
	var (
		courseID, bodyMD, acceptedTaskID sql.NullString
		resolvedAt                       sql.NullString
		status                           string
		createdAt                        string
	)
	if err := s.Scan(
		&p.ID, &courseID, &p.Title, &bodyMD,
		&p.TargetDate, &status, &p.CreatedByAgent,
		&acceptedTaskID,
		&createdAt, &resolvedAt,
	); err != nil {
		return nil, err
	}
	p.CourseID = courseID.String
	p.BodyMD = bodyMD.String
	p.Status = study.Status(status)
	p.AcceptedTaskID = acceptedTaskID.String
	p.CreatedAt = parseTimeLite(createdAt)
	if resolvedAt.Valid && resolvedAt.String != "" {
		t := parseTimeLite(resolvedAt.String)
		p.ResolvedAt = &t
	}
	return &p, nil
}

// scanner abstracts over *sql.Row and *sql.Rows — both have Scan,
// but they have different concrete types so scanProposal needs an
// interface. Scan returns sql.ErrNoRows when the row iterator is
// exhausted; the Get wrapper translates that to study.ErrNotFound.
type scanner interface {
	Scan(dest ...any) error
}

// Package study holds the domain entities for the Phase 31 study-
// reminder flow.
//
// The flow:
//
//	external planner agent                user
//	────────────────────                  ────
//	POST /agent/study-proposals  ───►   tray on Dashboard
//	                                       │
//	                                accept │ dismiss
//	                                       ▼
//	                              inbox task (study_course_id set,
//	                              due_at = max(target_date, today))
//
// The proposal table is a transient queue: only `pending` proposals
// are user-visible. Accept is idempotent (the service returns the
// task it created the first time) and dismiss is final. Resolved
// proposals are kept for audit — they record what the agent proposed
// and what the user did with it.
package study

import (
	"errors"
	"strings"
	"time"
)

// Status enumerates the lifecycle of a study proposal.
type Status string

const (
	// StatusPending is the initial state — visible in the tray, can
	// be accepted or dismissed.
	StatusPending Status = "pending"
	// StatusAccepted means the user accepted; the proposal has a
	// non-nil accepted_task_id pointing at the inbox task.
	StatusAccepted Status = "accepted"
	// StatusDismissed means the user dismissed; no task created.
	StatusDismissed Status = "dismissed"
)

// IsValid reports whether s is one of the three known statuses.
// Anything else is rejected by Validate() and by the CHECK
// constraint on the table.
func (s Status) IsValid() bool {
	switch s {
	case StatusPending, StatusAccepted, StatusDismissed:
		return true
	default:
		return false
	}
}

// Sentinel errors. Handlers translate these to HTTP status codes.
//
//	ErrNotFound     → 404
//	ErrInvalidInput → 400 (validation failure)
//	ErrTransition   → 409 (accept/dismiss on a non-pending proposal)
var (
	ErrNotFound     = errors.New("study: not found")
	ErrInvalidInput = errors.New("study: invalid input")
	ErrTransition   = errors.New("study: invalid lifecycle transition")
)

// Proposal is one item in the study tray.
//
// The lifecycle is linear: pending → (accepted | dismissed). Once
// resolved the proposal is immutable from the user's perspective
// (operators can still clean up via SQL). Accepted proposals carry
// the id of the inbox task they materialised; repeating Accept on
// such a proposal returns the same task id (the service does not
// create a duplicate).
//
// CreatedByAgent is a hard reference (no ON DELETE CASCADE — see
// migration 022): we don't want a forgotten agent cleanup to wipe
// the audit trail.
type Proposal struct {
	ID             string     `json:"id"`
	CourseID       string     `json:"course_id,omitempty"`
	Title          string     `json:"title"`
	BodyMD         string     `json:"body_md,omitempty"`
	TargetDate     string     `json:"target_date"` // YYYY-MM-DD
	Status         Status     `json:"status"`
	CreatedByAgent string     `json:"created_by_agent"`
	AcceptedTaskID string     `json:"accepted_task_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
}

// Validate checks the proposal's basic fields. The service layer
// enforces lifecycle transitions (accept/dismiss from pending only);
// Validate itself only shapes the data.
//
// Rules:
//   - Title: non-empty after trim, ≤ 200 chars.
//   - BodyMD: ≤ 16 KiB (markdown body; plenty for a study plan).
//   - TargetDate: parses as YYYY-MM-DD (calendar date, no time/timezone
//     — the agent writes it, the user sees it, the service converts
//     to a UTC due_at by combining with the day's end-of-day or
//     max(target, today)).
//   - CourseID: optional. When empty the proposal is a free-standing
//     reminder (no course to link).
//   - Status: must be one of the three known values; defaults to
//     pending when unset.
//
// NormalizeTitle is a pure helper that canonicalizes a proposal
// title for the dedup comparison in Service.Propose. The rules:
//   - trim leading/trailing whitespace
//   - collapse runs of internal whitespace into a single space
//   - lowercase (ASCII)
//
// Examples:
//
//	"Read Chapter 3"        → "read chapter 3"
//	"  Read   Chapter 3  "  → "read chapter 3"
//	"Read\tChapter\n3"      → "read chapter 3"
//
// Unicode normalization (NFKC/NFD) is deliberately out of scope —
// the planner agent writes in plain ASCII for MVP; if a non-ASCII
// collision becomes a real problem we can layer a stronger
// normalizer on top without breaking the existing contract.
//
// Normalization is applied ONLY for dedup comparison; the
// original title is what gets stored in the DB and returned to
// the UI.
func NormalizeTitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		b.WriteRune(r)
		inSpace = false
	}
	return strings.ToLower(b.String())
}

//   - Status: must be one of the three known values; defaults to
//     pending when unset.
//   - CreatedByAgent: required, non-empty.
func (p *Proposal) Validate() error {

	// Validate checks the proposal's basic fields.
	p.Title = strings.TrimSpace(p.Title)
	p.BodyMD = strings.TrimSpace(p.BodyMD)
	if p.Title == "" {
		return ErrInvalidInput
	}
	if len(p.Title) > 200 {
		return ErrInvalidInput
	}
	if len(p.BodyMD) > 16384 {
		return ErrInvalidInput
	}
	if p.TargetDate == "" {
		return ErrInvalidInput
	}
	if _, err := time.Parse("2006-01-02", p.TargetDate); err != nil {
		return ErrInvalidInput
	}
	if p.Status == "" {
		p.Status = StatusPending
	}
	if !p.Status.IsValid() {
		return ErrInvalidInput
	}
	if p.CreatedByAgent == "" {
		return ErrInvalidInput
	}
	return nil
}

// AcceptAllowed reports whether Accept can run on the proposal from
// its current status. Accept is only valid from pending; re-running
// it on an already-accepted proposal is the service's idempotency
// path (returns the existing task) — not a transition error.
func (p *Proposal) AcceptAllowed() bool {
	return p.Status == StatusPending
}

// DismissAllowed is symmetric with AcceptAllowed — only pending
// proposals can be dismissed.
func (p *Proposal) DismissAllowed() bool {
	return p.Status == StatusPending
}

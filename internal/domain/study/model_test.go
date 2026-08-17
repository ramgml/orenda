package study_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/study"
)

// TestStatus_IsValid pins the three known statuses. Anything else
// is rejected by the CHECK constraint on the table AND by Validate.
func TestStatus_IsValid(t *testing.T) {
	for _, s := range []study.Status{study.StatusPending, study.StatusAccepted, study.StatusDismissed} {
		assert.True(t, s.IsValid(), "expected %s to be valid", s)
	}
	for _, bad := range []study.Status{"", "Pending", "PENDING", "in_progress", "done"} {
		assert.False(t, bad.IsValid(), "%q should be rejected", bad)
	}
}

// TestProposal_Validate_HappyPath: a minimal valid proposal. Title
// trimmed, defaults applied.
func TestProposal_Validate_HappyPath(t *testing.T) {
	p := &study.Proposal{
		Title:          "Read Rust chapter 5",
		TargetDate:     "2026-08-17",
		CreatedByAgent: "agent-planner",
	}
	require.NoError(t, p.Validate())
	assert.Equal(t, study.StatusPending, p.Status, "empty status defaults to pending")
	assert.Equal(t, "Read Rust chapter 5", p.Title, "Title kept verbatim when no leading/trailing whitespace")
}

// TestProposal_Validate_DefaultsAndTrim covers the trim/default path
// and verifies that whitespace around the title is normalised.
func TestProposal_Validate_DefaultsAndTrim(t *testing.T) {
	p := &study.Proposal{
		Title:          "  study tomorrow  \n",
		TargetDate:     "2026-08-17",
		CreatedByAgent: "agent-planner",
	}
	require.NoError(t, p.Validate())
	assert.Equal(t, "study tomorrow", p.Title, "leading/trailing whitespace is trimmed")
}

// TestProposal_Validate_Errors: every shape violation goes through
// ErrInvalidInput.
func TestProposal_Validate_Errors(t *testing.T) {
	tests := []struct {
		name string
		mut  func(p *study.Proposal)
	}{
		{"missing title", func(p *study.Proposal) {
			p.Title = ""
			p.TargetDate = "2026-08-17"
			p.CreatedByAgent = "a"
		}},
		{"whitespace-only title", func(p *study.Proposal) {
			p.Title = "   \n\t  "
			p.TargetDate = "2026-08-17"
			p.CreatedByAgent = "a"
		}},
		{"title too long", func(p *study.Proposal) {
			p.Title = strings.Repeat("x", 201)
			p.TargetDate = "2026-08-17"
			p.CreatedByAgent = "a"
		}},
		{"body too long", func(p *study.Proposal) {
			p.Title = "ok"
			p.BodyMD = strings.Repeat("x", 16385)
			p.TargetDate = "2026-08-17"
			p.CreatedByAgent = "a"
		}},
		{"missing target_date", func(p *study.Proposal) {
			p.Title = "ok"
			p.TargetDate = ""
			p.CreatedByAgent = "a"
		}},
		{"malformed target_date", func(p *study.Proposal) {
			p.Title = "ok"
			p.TargetDate = "2026/08/17" // wrong shape
			p.CreatedByAgent = "a"
		}},
		{"invalid status", func(p *study.Proposal) {
			p.Title = "ok"
			p.TargetDate = "2026-08-17"
			p.CreatedByAgent = "a"
			p.Status = "PENDING" // wrong case — not in the closed set
		}},
		{"missing agent", func(p *study.Proposal) {
			p.Title = "ok"
			p.TargetDate = "2026-08-17"
			p.CreatedByAgent = ""
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &study.Proposal{}
			tc.mut(p)
			require.Error(t, p.Validate())
		})
	}
}

// TestProposal_AcceptDismissAllowed pins the lifecycle: only
// pending is mutable. Re-running accept/dismiss on a resolved
// proposal returns false at the domain layer (the service layer
// handles the idempotency path separately).
func TestProposal_AcceptDismissAllowed(t *testing.T) {
	cases := []struct {
		status                 study.Status
		accept, dismissAllowed bool
	}{
		{study.StatusPending, true, true},
		{study.StatusAccepted, false, false},
		{study.StatusDismissed, false, false},
	}
	for _, tc := range cases {
		p := &study.Proposal{Status: tc.status}
		assert.Equal(t, tc.accept, p.AcceptAllowed(), "status=%s AcceptAllowed", tc.status)
		assert.Equal(t, tc.dismissAllowed, p.DismissAllowed(), "status=%s DismissAllowed", tc.status)
	}
}

// TestProposal_TargetDate_AcceptsValidDates — boundary test for
// the date parser: month boundaries and leap-day sanity.
func TestProposal_TargetDate_AcceptsValidDates(t *testing.T) {
	for _, date := range []string{"2026-01-01", "2026-12-31", "2024-02-29", "2026-08-17"} {
		p := &study.Proposal{
			Title:          "ok",
			TargetDate:     date,
			CreatedByAgent: "a",
		}
		require.NoError(t, p.Validate(), "date %s should be accepted", date)
	}
}

// TestProposal_TargetDate_RejectsInvalidDates — bad dates must fail
// Validate. Notably "2026-02-30" (no such day) and "2026-13-01"
// (no such month) should be rejected.
func TestProposal_TargetDate_RejectsInvalidDates(t *testing.T) {
	for _, date := range []string{"2026-02-30", "2026-13-01", "2026-08-17T10:00", "17-08-2026"} {
		p := &study.Proposal{
			Title:          "ok",
			TargetDate:     date,
			CreatedByAgent: "a",
		}
		require.Error(t, p.Validate(), "date %s should be rejected", date)
	}
}

// TestProposal_ResolvedAt_SetOnResolve is a tiny smoke for the
// optional timestamp field — Validate must not require it (only
// the lifecycle check enforces it, and Validate runs on
// construction before resolution).
func TestProposal_ResolvedAt_SetOnResolve(t *testing.T) {
	now := time.Now().UTC()
	p := &study.Proposal{
		Title:          "ok",
		TargetDate:     "2026-08-17",
		CreatedByAgent: "a",
		Status:         study.StatusAccepted,
		ResolvedAt:     &now,
	}
	require.NoError(t, p.Validate())
	require.NotNil(t, p.ResolvedAt)
}

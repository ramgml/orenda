// Phase 30.14: tests for the column-status slug helpers used by
// CreateColumn and (indirectly) by the migration-020 backfill.
package sqlite

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSlugifyColumnStatus(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Canonical names stay verbatim (agent flow hard-codes them).
		{"backlog", "backlog", "backlog"},
		{"todo", "todo", "todo"},
		{"in_progress canonical", "in_progress", "in_progress"},
		{"review", "review", "review"},
		{"done", "done", "done"},
		// Empty / whitespace => "custom" fallback.
		{"empty", "", "custom"},
		{"only spaces", "   ", "custom"},
		{"only punctuation", "---", "custom"},
		// Lowercase + non-alnum → '_' with runs collapsed.
		{"simple", "My Column", "my_column"},
		{"in_progress custom name", "In Progress", "in_progress"},
		{"dashes collapse", "QA-pass", "qa_pass"},
		{"slashes collapse", "Stage 1 / Stage 2", "stage_1_stage_2"},
		{"unicode stripped", "Привет", "custom"},
		{"with digits", "Sprint 12", "sprint_12"},
		{"leading digit lowercase -> trim keeps '_'", "1 first", "1_first"},
		// Trailing / leading punctuation trimmed.
		{"punctuation edges", "...foo...", "foo"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, slugifyColumnStatus(tc.in), tc.name)
	}
}

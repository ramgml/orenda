package course

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Phase 31: pace_notes_md validation. The notes are free-form
// markdown that the agent-planner reads when proposing study tasks;
// the user can also edit them in the UI. Validate trims whitespace
// and rejects oversized payloads.
func TestCourse_Validate_PaceNotesMD(t *testing.T) {
	t.Run("accepts empty", func(t *testing.T) {
		c := &Course{Title: "Rust", Status: StatusDraft, PaceNotesMD: ""}
		require.NoError(t, c.Validate())
		assert.Equal(t, "", c.PaceNotesMD)
	})

	t.Run("trims surrounding whitespace", func(t *testing.T) {
		c := &Course{Title: "Rust", Status: StatusDraft, PaceNotesMD: "  3 times a week  \n"}
		require.NoError(t, c.Validate())
		assert.Equal(t, "3 times a week", c.PaceNotesMD)
	})

	t.Run("rejects oversized notes", func(t *testing.T) {
		c := &Course{
			Title:       "Rust",
			Status:      StatusDraft,
			PaceNotesMD: strings.Repeat("x", 65537),
		}
		require.Error(t, c.Validate())
	})

	t.Run("accepts boundary size", func(t *testing.T) {
		// Exactly 64 KiB is the cap; everything equal-or-below is
		// accepted (the validator is non-strict on the upper bound).
		c := &Course{
			Title:       "Rust",
			Status:      StatusDraft,
			PaceNotesMD: strings.Repeat("x", 65536),
		}
		require.NoError(t, c.Validate())
	})

	t.Run("existing rules still apply", func(t *testing.T) {
		// PaceNotesMD is fine but missing title must still fail.
		c := &Course{Status: StatusDraft, PaceNotesMD: "ok"}
		require.Error(t, c.Validate())
	})
}

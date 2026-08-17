package task_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// Phase 31: StudyCourseID is a loose FK marker — the DB enforces
// referential integrity, the domain layer only checks that other
// invariants still hold. This test pins that adding the field
// doesn't break Validate for any of the existing shapes.
func TestTask_Validate_WithStudyCourseID(t *testing.T) {
	t.Run("plain task with study_course_id", func(t *testing.T) {
		tr := &task.Task{
			Title:         "Read chapter 5",
			ProjectID:     "proj-1",
			StudyCourseID: "course-1",
		}
		require.NoError(t, tr.Validate())
		assert.Equal(t, "course-1", tr.StudyCourseID)
	})

	t.Run("inbox task with study_course_id", func(t *testing.T) {
		// Inbox tasks have no project and no column; the study
		// marker is independent. Reminders live in the tray as
		// inbox tasks until the user files them under a project.
		tr := &task.Task{
			Title:         "Study caps",
			StudyCourseID: "course-1",
		}
		require.NoError(t, tr.Validate())
	})

	t.Run("empty study_course_id is the unmarked case", func(t *testing.T) {
		tr := &task.Task{Title: "x", ProjectID: "p"}
		require.NoError(t, tr.Validate())
		assert.Equal(t, "", tr.StudyCourseID)
	})
}

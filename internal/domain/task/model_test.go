package task_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

func TestStatus_IsValid(t *testing.T) {
	for _, s := range task.AllStatuses {
		assert.True(t, s.IsValid(), "expected %s to be valid", s)
		assert.True(t, s.IsCanonical(), "expected %s to be canonical", s)
	}
	assert.True(t, task.Status("cancelled").IsValid(),
		"non-canonical machine key satisfying the regex must be valid (Phase 30.14: custom column statuses)")
	assert.False(t, task.Status("").IsValid())
	// Custom machine keys: lowercase letter + [a-z0-9_]*.
	for _, ok := range []task.Status{"cancelled", "in_qa", "awaiting_review", "x9"} {
		assert.True(t, ok.IsValid(), "machine key %q should be valid", ok)
		assert.False(t, ok.IsCanonical(), "machine key %q should not be canonical", ok)
	}
	// Invalid shapes.
	for _, bad := range []task.Status{"Done", "1in_progress", "in-progress", "in progress", "in.progress", "in;progress", "DROP TABLE"} {
		assert.False(t, bad.IsValid(), "shape %q must be rejected", bad)
	}
	assert.False(t, task.Status("").IsCanonical())
}

// T376 (PRD F-T-3): `rejected` is a first-class canonical status —
// canonical-validation sites (PATCH, bulk, CLI, MCP, mirror labels) all
// read AllStatuses/CanonicalStatus, and the parking column's position
// is pinned AFTER done (before the auto-only blocked) so every render
// and migration site agrees on the order.
func TestStatus_RejectedIsCanonicalAfterDone(t *testing.T) {
	assert.Contains(t, task.AllStatuses, task.StatusRejected)

	gotCanonical, ok := task.CanonicalStatus("rejected")
	require.True(t, ok, "rejected must resolve via CanonicalStatus")
	assert.Equal(t, task.StatusRejected, gotCanonical)
	assert.True(t, task.StatusRejected.IsCanonical())
	assert.True(t, task.StatusRejected.IsValid())

	// Order contract: … done, rejected, blocked.
	require.GreaterOrEqual(t, len(task.AllStatuses), 3)
	assert.Equal(t, task.StatusDone, task.AllStatuses[len(task.AllStatuses)-3])
	assert.Equal(t, task.StatusRejected, task.AllStatuses[len(task.AllStatuses)-2])
	assert.Equal(t, task.StatusBlocked, task.AllStatuses[len(task.AllStatuses)-1])
}

func TestTask_Validate_Defaults(t *testing.T) {
	tr := &task.Task{
		Title:     "Implement X",
		ProjectID: "proj-1",
	}
	require.NoError(t, tr.Validate())
	assert.Equal(t, task.StatusTodo, tr.Status)
	assert.Equal(t, task.PriorityMedium, tr.Priority)
	assert.Equal(t, task.AwaitingNone, tr.Awaiting)
}

func TestTask_Validate_Errors(t *testing.T) {
	tests := []struct {
		name string
		mut  func(t *task.Task)
	}{
		{"missing title", func(t *task.Task) { t.ProjectID = "p" }},
		{"inbox with column", func(t *task.Task) {
			// Phase 16: empty ProjectID is allowed (Inbox) but the
			// task must not also have a ColumnID — the inbox has no
			// board, so column_id must be NULL.
			t.Title = "x"
			t.ProjectID = ""
			t.ColumnID = "col-1"
		}},
		{"invalid status", func(t *task.Task) {
			// "weird" is a valid machine key (Phase 27.8.4 + 30.14),
			// so we exercise the shape-check rejection path instead:
			// leading digit / spaces / uppercase violate the regex.
			t.Title = "x"
			t.ProjectID = "p"
			t.Status = "weird status" // space is rejected
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := &task.Task{}
			tc.mut(tr)
			require.Error(t, tr.Validate())
		})
	}
}

// Phase 16: an Inbox task (no project, no column) is valid.
func TestTask_Validate_InboxTask(t *testing.T) {
	tr := &task.Task{Title: "capture me"}
	require.NoError(t, tr.Validate())
}

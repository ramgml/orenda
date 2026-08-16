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
	}
	assert.False(t, task.Status("cancelled").IsValid())
	assert.False(t, task.Status("").IsValid())
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
			t.Title = "x"
			t.ProjectID = "p"
			t.Status = "weird"
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

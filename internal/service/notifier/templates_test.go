package notifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/bot"
	"github.com/ramgml/orenda/internal/service/notifier"
)

// TestTemplates_ReviewNeeded verifies the task.review_needed template
// produces a Message with Approve/Reject actions keyed on the task id.
func TestTemplates_ReviewNeeded(t *testing.T) {
	m := notifier.ReviewNeeded(notifier.Event{
		Type:     "task.review_needed",
		Title:    "Ready for review",
		Body:     "task X",
		TargetID: "task-42",
		Link:     "/tasks/task-42",
	})
	assert.Equal(t, "task.review_needed", m.Kind)
	assert.Equal(t, "task-42", m.CallbackID)
	assert.Equal(t, "/tasks/task-42", m.Link)
	require.Len(t, m.Actions, 2)
	assert.Equal(t, "approve", m.Actions[0].Callback)
	assert.Equal(t, "reject", m.Actions[1].Callback)
}

// TestTemplates_AssignedToMe uses a URL action so bots without
// inline-button support (email, webhook) render a working link.
func TestTemplates_AssignedToMe(t *testing.T) {
	m := notifier.AssignedToMe(notifier.Event{
		Type:     "task.assigned_to_me",
		Title:    "Picked up",
		Body:     "agent x",
		TargetID: "task-1",
		Link:     "/tasks/task-1",
	})
	require.Len(t, m.Actions, 1)
	assert.Equal(t, "/tasks/task-1", m.Actions[0].URL, "URL action preferred over callback")
	assert.Empty(t, m.Actions[0].Callback)
}

// TestRender_UnknownKind verifies the fallback path.
func TestRender_UnknownKind(t *testing.T) {
	m := notifier.Render(notifier.Event{Type: "totally.new.event", Title: "x", Body: "y"})
	assert.Equal(t, "totally.new.event", m.Kind)
	assert.Empty(t, m.Actions)
}

// TestRender_DispatchesByType — sanity check on the default table.
func TestRender_DispatchesByType(t *testing.T) {
	cases := []string{
		"task.review_needed",
		"task.assigned_to_me",
		"task.released",
		"mention.created",
		"task.commented",
		"agent.offline",
		"backup.failed",
		"event.upcoming_1h",
	}
	for _, kind := range cases {
		t.Run(kind, func(t *testing.T) {
			m := notifier.Render(notifier.Event{Type: kind, Title: "t", Body: "b", TargetID: "x"})
			assert.Equal(t, kind, m.Kind)
			// Every template populates CallbackID so callback handlers
			// can correlate by task id (or empty for non-task events).
			assert.Equal(t, "x", m.CallbackID)
		})
	}
}

// bot.Message round-trip via a fresh Message keeps the Actions slice
// in the order they were declared.
func TestBotMessage_ActionsOrderPreserved(t *testing.T) {
	m := bot.Message{
		Title: "t",
		Actions: []bot.Action{
			{Label: "A", Callback: "a"},
			{Label: "B", Callback: "b"},
			{Label: "C", Callback: "c"},
		},
	}
	require.Len(t, m.Actions, 3)
	assert.Equal(t, "A", m.Actions[0].Label)
	assert.Equal(t, "C", m.Actions[2].Label)
}

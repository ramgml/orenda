// Phase 27.9: end-to-end coverage for the WS + activity hooks the
// courseTaskCreatorAdapter now publishes. Pre-27.9 the adapter
// intentionally skipped these channels; live UI was blind to
// course-spawned tasks (no review-queue badge, no kanban update
// until a manual refetch).
//
// We exercise the adapter against a real SQLite DB + an in-memory
// recording hub + a fake recorder so the wiring path is realistic
// without spinning up a full server.
package main

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	activitydomain "github.com/ramgml/orenda/internal/domain/activity"
	coursedomain "github.com/ramgml/orenda/internal/domain/course"
	taskdomain "github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// recordingHub captures every Publish call so the test can assert on
// topic + payload. Subscribe is a no-op — the test doesn't read.
type recordingHub struct {
	mu     sync.Mutex
	events []ws.Event
}

func (h *recordingHub) Publish(_ context.Context, e ws.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, e)
}

func (h *recordingHub) Close() {}

func (h *recordingHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event)
	return ch, func() {}
}

func (h *recordingHub) Topics() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.events))
	for i, e := range h.events {
		out[i] = e.Topic
	}
	return out
}

// recordingRecorder captures every RecordTask call so the test can
// assert on action + payload. Mirrors the production interface so
// the adapter's wiring is exercised end-to-end.
type recordingRecorder struct {
	mu       sync.Mutex
	recorded []recordedActivity
}

type recordedActivity struct {
	TaskID  string
	Actor   activitydomain.ActorType
	ActorID string
	Action  activitydomain.Action
	Payload string
}

func (r *recordingRecorder) RecordTask(_ context.Context, taskID string, actor activitydomain.ActorType, actorID string, action activitydomain.Action, payload string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recorded = append(r.recorded, recordedActivity{
		TaskID: taskID, Actor: actor, ActorID: actorID, Action: action, Payload: payload,
	})
	return nil
}

func setupCourseAdapter(t *testing.T) (courseTaskCreatorAdapter, *recordingHub, *recordingRecorder, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "ca.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	owner := &user.User{
		Email:        "ca-owner-" + t.Name() + "@x",
		PasswordHash: "x", DisplayName: "Ca",
	}
	require.NoError(t, users.Create(context.Background(), owner))

	tasksRepo := sqlite.NewTaskRepository(db)
	courseRepo := sqlite.NewCourseRepository(db)

	// Seed a course + module + lesson + quiz so CreateQuizReviewTask
	// has a real quiz to look up (it joins course_quizzes +
	// course_lessons). GeneratorTask only needs the owner/course id.
	c := &coursedomain.Course{
		Title:    "Learn Vim",
		IntentMD: "test",
		Status:   coursedomain.StatusDraft,
		OwnerID:  owner.ID,
	}
	require.NoError(t, courseRepo.CreateCourse(context.Background(), c))
	m := &coursedomain.Module{CourseID: c.ID, Title: "Basics", Position: 0}
	require.NoError(t, courseRepo.CreateModule(context.Background(), m))
	l := &coursedomain.Lesson{ModuleID: m.ID, Title: "Modes", Position: 0, Status: coursedomain.LessonLocked}
	require.NoError(t, courseRepo.CreateLesson(context.Background(), l))
	q := &coursedomain.Quiz{
		LessonID: l.ID, Position: 0,
		QuestionMD: "What command enters insert mode?",
		ExpectedMD: "i",
		Kind:       coursedomain.QuizExact,
	}
	require.NoError(t, courseRepo.CreateQuiz(context.Background(), q))

	hub := &recordingHub{}
	rec := &recordingRecorder{}
	adapter := courseTaskCreatorAdapter{
		tasksRepo: tasksRepo,
		db:        db,
		hub:       hub,
		recorder:  rec,
	}
	return adapter, hub, rec, q.ID + "|" + l.ID + "|" + owner.ID
}

func TestCourseAdapter_CreateGeneratorTask_PublishesAndRecords(t *testing.T) {
	adapter, hub, rec, _ := setupCourseAdapter(t)

	id, err := adapter.CreateGeneratorTask(context.Background(), "owner-1", "course-1", "Learn Rust", "intense")
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	// WS: one "tasks" event with task.created source.
	hub.mu.Lock()
	defer hub.mu.Unlock()
	require.Len(t, hub.events, 1)
	assert.Equal(t, "tasks", hub.events[0].Topic)
	body, ok := hub.events[0].Body.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "task.created", body["type"])
	assert.Equal(t, "course_generator", body["source"])

	// Activity: one row with action=created, payload referencing the
	// generator source so future audits can tell apart agent-driven
	// task creation from system-driven course wiring.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.recorded, 1)
	assert.Equal(t, id, rec.recorded[0].TaskID)
	assert.Equal(t, activitydomain.ActionCreated, rec.recorded[0].Action)
	assert.Contains(t, rec.recorded[0].Payload, "course_generator")
}

// TestCourseAdapter_CreateQuizReviewTask_PublishesAndRecords covers the
// other entry point the adapter exposes — the open-quiz answer
// spawns a review task with quiz+lesson context baked into the title.
func TestCourseAdapter_CreateQuizReviewTask_PublishesAndRecords(t *testing.T) {
	adapter, hub, rec, ids := setupCourseAdapter(t)
	parts := splitQuizTriple(t, ids)
	quizID, lessonID, _ := parts[0], parts[1], parts[2]

	id, err := adapter.CreateQuizReviewTask(context.Background(), "owner-1", quizID, lessonID, "wrong answer")
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	hub.mu.Lock()
	defer hub.mu.Unlock()
	require.Len(t, hub.events, 1)
	body, ok := hub.events[0].Body.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "course_quiz_review", body["source"])

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.recorded, 1)
	assert.Contains(t, rec.recorded[0].Payload, "course_quiz_review")
}

// TestCourseAdapter_NilHubAndRecorder_DoesNotPanic guards the "best
// effort" contract: when a minimal configuration omits hub or
// recorder (a future test harness, a partial bring-up), the adapter
// must still create the task row without panicking.
func TestCourseAdapter_NilHubAndRecorder_DoesNotPanic(t *testing.T) {
	adapter, _, _, _ := setupCourseAdapter(t)
	adapter.hub = nil
	adapter.recorder = nil

	id, err := adapter.CreateGeneratorTask(context.Background(), "owner-1", "course-1", "Learn Go", "test")
	require.NoError(t, err)
	assert.NotEmpty(t, id, "task id is still returned even when observability hooks are nil")
}

// splitQuizTriple parses the "|"-joined id triple returned by
// setupCourseAdapter. Lives here because it's only used by these
// tests.
func splitQuizTriple(t *testing.T, s string) []string {
	t.Helper()
	out := split3(s, "|")
	require.Len(t, out, 3)
	return out
}

func split3(s, sep string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if string(s[i]) == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// Ensure the test file references taskdomain so go vet stays quiet
// if we trim imports during refactors.
var _ taskdomain.Status = ""

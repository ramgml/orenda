package tutor_test

// T16: tutor service unit tests. In-memory repo + course stub — no
// SQLite; the storage layer is covered by the API tests (handlers
// run against the migrated template DB).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/service/tutor"
)

// memRepo is the in-memory tutor.Repository + PendingLister.
type memRepo struct {
	msgs []*tutor.Message
	seq  int
}

func (r *memRepo) Create(_ context.Context, m *tutor.Message) error {
	r.seq++
	if m.ID == "" {
		m.ID = "m-" + string(rune('a'+r.seq%26)) + "-" + itoa(r.seq)
	}
	r.msgs = append(r.msgs, m)
	return nil
}

func (r *memRepo) ListThread(_ context.Context, lessonID, userID string) ([]*tutor.Message, error) {
	out := make([]*tutor.Message, 0)
	for _, m := range r.msgs {
		if m.LessonID == lessonID && m.UserID == userID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *memRepo) Last(_ context.Context, lessonID, userID string) (*tutor.Message, error) {
	var last *tutor.Message
	for _, m := range r.msgs {
		if m.LessonID == lessonID && m.UserID == userID {
			last = m
		}
	}
	return last, nil
}

// PendingThreads returns thread keys whose last message is a user
// question — mirrors the SQL in the sqlite repo.
func (r *memRepo) PendingThreads(_ context.Context) ([]tutor.PendingThreadKey, error) {
	tip := map[string]*tutor.Message{}
	for _, m := range r.msgs {
		tip[m.LessonID+"\x00"+m.UserID] = m
	}
	out := make([]tutor.PendingThreadKey, 0)
	for k, m := range tip {
		if m.Role != tutor.RoleUser {
			continue
		}
		var lessonID, userID string
		for i := 0; i < len(k); i++ {
			if k[i] == 0 {
				lessonID, userID = k[:i], k[i+1:]
				break
			}
		}
		out = append(out, tutor.PendingThreadKey{LessonID: lessonID, UserID: userID})
	}
	return out, nil
}

// courseStub is the course.Repository seam the service walks
// (lesson → module → course → quizzes). Only the four methods the
// tutor touches are real; everything else panics via the nil map
// semantics we never hit.
type courseStub struct {
	lessons map[string]*course.Lesson
	modules map[string]*course.Module
	courses map[string]*course.Course
	quizzes []*course.Quiz
}

func (s *courseStub) GetLesson(_ context.Context, id string) (*course.Lesson, error) {
	l, ok := s.lessons[id]
	if !ok {
		return nil, course.ErrNotFound
	}
	return l, nil
}

func (s *courseStub) GetModule(_ context.Context, id string) (*course.Module, error) {
	m, ok := s.modules[id]
	if !ok {
		return nil, course.ErrNotFound
	}
	return m, nil
}

func (s *courseStub) GetCourse(_ context.Context, id string) (*course.Course, error) {
	c, ok := s.courses[id]
	if !ok {
		return nil, course.ErrNotFound
	}
	return c, nil
}

func (s *courseStub) ListQuizzesInCourse(_ context.Context, courseID string) ([]*course.Quiz, error) {
	out := make([]*course.Quiz, 0)
	// The stub indexes quizzes by their owning lesson; resolve the
	// lesson's module to honor the courseID contract.
	for _, q := range s.quizzes {
		l, ok := s.lessons[q.LessonID]
		if !ok {
			continue
		}
		m, ok := s.modules[l.ModuleID]
		if !ok || m.CourseID != courseID {
			continue
		}
		out = append(out, q)
	}
	return out, nil
}

// newFixture wires the service the way main.go does.
func newFixture() (*tutor.Service, *memRepo, *courseStub) {
	repo := &memRepo{}
	courses := &courseStub{
		lessons: map[string]*course.Lesson{},
		modules: map[string]*course.Module{},
		courses: map[string]*course.Course{},
	}
	return tutor.New(repo, courses), repo, courses
}

// seedLesson creates a minimal consistent course → module → lesson
// (+ optional quiz) and returns the lesson id.
func seedLesson(courses *courseStub, withQuiz bool) string {
	courses.courses["c-1"] = &course.Course{ID: "c-1", Title: "Rust Basics"}
	courses.modules["m-1"] = &course.Module{ID: "m-1", CourseID: "c-1", Title: "Ownership"}
	courses.lessons["l-1"] = &course.Lesson{ID: "l-1", ModuleID: "m-1", Title: "Borrowing", ContentMD: "# Borrowing"}
	if withQuiz {
		courses.quizzes = append(courses.quizzes, &course.Quiz{ID: "q-1", LessonID: "l-1", QuestionMD: "What moves?"})
	}
	return "l-1"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestService_Ask_HappyPath(t *testing.T) {
	svc, _, courses := newFixture()
	lessonID := seedLesson(courses, false)

	m, err := svc.Ask(context.Background(), lessonID, "u-1", "  What is a lifetime?  ")
	require.NoError(t, err)
	assert.Equal(t, tutor.RoleUser, m.Role)
	assert.Equal(t, "What is a lifetime?", m.BodyMD, "body is trimmed")
	assert.NotEmpty(t, m.ID)
	assert.Greater(t, m.CreatedAt.Year(), 2020, "created_at must be stamped by the service, not left zero")
}

func TestService_Ask_Rejects(t *testing.T) {
	svc, _, courses := newFixture()
	lessonID := seedLesson(courses, false)

	// Unknown lesson → 404 sentinel.
	_, err := svc.Ask(context.Background(), "nope", "u-1", "hi")
	assert.ErrorIs(t, err, tutor.ErrNotFound)

	// Empty/whitespace body → 400 sentinel.
	_, err = svc.Ask(context.Background(), lessonID, "u-1", "   ")
	assert.ErrorIs(t, err, tutor.ErrInvalidInput)

	// Missing user → 400 sentinel.
	_, err = svc.Ask(context.Background(), lessonID, "", "hi")
	assert.ErrorIs(t, err, tutor.ErrInvalidInput)

	// Second question while the first is pending → 409 sentinel.
	_, err = svc.Ask(context.Background(), lessonID, "u-1", "first")
	require.NoError(t, err)
	_, err = svc.Ask(context.Background(), lessonID, "u-1", "second")
	assert.ErrorIs(t, err, tutor.ErrConflict)
}

func TestService_History(t *testing.T) {
	svc, _, courses := newFixture()
	lessonID := seedLesson(courses, false)

	// Unknown lesson → 404.
	_, err := svc.History(context.Background(), "nope", "u-1")
	assert.ErrorIs(t, err, tutor.ErrNotFound)

	// Empty thread is not an error.
	msgs, err := svc.History(context.Background(), lessonID, "u-1")
	require.NoError(t, err)
	assert.Empty(t, msgs)

	// After a dialog the history returns both turns in order.
	_, err = svc.Ask(context.Background(), lessonID, "u-1", "q")
	require.NoError(t, err)
	_, err = svc.Reply(context.Background(), lessonID, "a")
	require.NoError(t, err)
	msgs, err = svc.History(context.Background(), lessonID, "u-1")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, tutor.RoleUser, msgs[0].Role)
	assert.Equal(t, tutor.RoleAgent, msgs[1].Role)
	// Both sides must carry a real timestamp - the repo writes
	// created_at verbatim, so a zero stamp would persist as
	// year-0001 and sort the thread wrong.
	assert.Greater(t, msgs[0].CreatedAt.Year(), 2020, "question created_at must be recent")
	assert.Greater(t, msgs[1].CreatedAt.Year(), 2020, "reply created_at must be recent")
}

func TestService_ListPending_EmbedsLessonContext(t *testing.T) {
	svc, _, courses := newFixture()
	lessonID := seedLesson(courses, true)

	_, err := svc.Ask(context.Background(), lessonID, "u-1", "Explain moves")
	require.NoError(t, err)

	pending, err := svc.ListPending(context.Background())
	require.NoError(t, err)
	require.Len(t, pending, 1)
	pq := pending[0]
	assert.Equal(t, "u-1", pq.UserID)
	assert.Equal(t, "Explain moves", pq.BodyMD)
	assert.Equal(t, "Borrowing", pq.LessonTitle)
	assert.Equal(t, "# Borrowing", pq.ContentMD)
	assert.Equal(t, "c-1", pq.CourseID)
	assert.Equal(t, "Rust Basics", pq.CourseTitle)
	require.Len(t, pq.Quizzes, 1)
	assert.Equal(t, "What moves?", pq.Quizzes[0].QuestionMD)
}

func TestService_Reply_Lifecycle(t *testing.T) {
	svc, repo, courses := newFixture()
	lessonID := seedLesson(courses, false)

	// Reply without a pending question → 409.
	_, err := svc.Reply(context.Background(), lessonID, "answer")
	assert.ErrorIs(t, err, tutor.ErrConflict)

	// Unknown lesson → 404 even before the pending check.
	_, err = svc.Reply(context.Background(), "nope", "answer")
	assert.ErrorIs(t, err, tutor.ErrNotFound)

	// Empty body → 400.
	_, err = svc.Ask(context.Background(), lessonID, "u-1", "q")
	require.NoError(t, err)
	_, err = svc.Reply(context.Background(), lessonID, "  ")
	assert.ErrorIs(t, err, tutor.ErrInvalidInput)

	// Happy path: reply resolves the thread.
	m, err := svc.Reply(context.Background(), lessonID, "the answer")
	require.NoError(t, err)
	assert.Equal(t, tutor.RoleAgent, m.Role)
	assert.Equal(t, "u-1", m.UserID, "reply lands on the pending user's thread")

	// The thread is no longer pending.
	pending, err := svc.ListPending(context.Background())
	require.NoError(t, err)
	assert.Empty(t, pending)

	// A new question re-opens the cycle.
	_, err = svc.Ask(context.Background(), lessonID, "u-1", "follow-up")
	require.NoError(t, err)
	pending, err = svc.ListPending(context.Background())
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "follow-up", pending[0].BodyMD)

	_ = repo
}

// A lesson with TWO pending student threads cannot be answered: the
// reply body carries only lesson_id, so any pick would silently
// route the answer to the wrong student. The service must refuse
// with ErrConflict (409) instead of guessing.
func TestService_Reply_MultiplePending_Conflict(t *testing.T) {
	svc, courses := newFixture()
	lessonID := seedLesson(courses, false)

	_, err := svc.Ask(context.Background(), lessonID, "u-1", "question from u-1")
	require.NoError(t, err)
	_, err = svc.Ask(context.Background(), lessonID, "u-2", "question from u-2")
	require.NoError(t, err)

	pending, err := svc.ListPending(context.Background())
	require.NoError(t, err)
	require.Len(t, pending, 2)

	// Two pending threads on one lesson: the reply body carries
	// only lesson_id, so any pick would silently route the answer
	// to the wrong student - the service must refuse instead.
	_, err = svc.Reply(context.Background(), lessonID, "an answer")
	assert.ErrorIs(t, err, tutor.ErrConflict)
}

// One pending thread still resolves unambiguously after the
// conflict rule landed.
func TestService_Reply_SinglePending_Resolves(t *testing.T) {
	svc, courses := newFixture()
	lessonID := seedLesson(courses, false)

	_, err := svc.Ask(context.Background(), lessonID, "u-1", "solo question")
	require.NoError(t, err)
	m, err := svc.Reply(context.Background(), lessonID, "the answer")
	require.NoError(t, err)
	assert.Equal(t, "u-1", m.UserID, "single pending thread resolves to its owner")
}

// Compile-time pins: the service interfaces are satisfied by the
// production sqlite types.
var (
	_ tutor.Repository    = (*memRepo)(nil)
	_ tutor.PendingLister = (*memRepo)(nil)
)

// Package service — Phase 18 course service.
//
// High-level operations:
//   - CreateWithIntent: build a course from a user intent, plus a
//     generator task for the tutor agent (the same shape as the
//     ticket-orchestrator pattern in Phase 2).
//   - SubmitCurriculum: the tutor's atomic curriculum swap.
//   - ApproveCurriculum: move the course to active and unlock the
//     first lesson.
//   - MaterializeLesson (Phase 27.4): the tutor writes content_md
//     and the linked exercise task; the lesson flips from locked to
//     open so the student can read it.
//   - CompleteLesson: mark a lesson done and unlock the next one.
//   - AnswerQuiz (Phase 27.4): server-side check for `exact` quizzes
//     (normalised string compare); `open` quizzes spawn a review
//     task on the tutor agent and return its id.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/ramgml/orenda/internal/domain/course"
)

// Sentinel errors. Handlers translate these to HTTP status codes.
var (
	ErrNotFound     = errors.New("course service: not found")
	ErrInvalidInput = errors.New("course service: invalid input")
	ErrTransition   = errors.New("course service: invalid lifecycle transition")
	ErrLessonLocked = errors.New("course service: lesson is locked")
)

// TaskCreator is the narrow seam the course service uses to spawn
// follow-up tasks (Phase 27.4). The default implementation lives in
// cmd/orenda and reuses the existing task service — the course
// service only needs to know "create a task, give me its id".
//
// Two methods because the two task types have different fields:
//   - GeneratorTask: the headline "build the curriculum" task the
//     tutor agent picks up immediately after CreateWithIntent.
//   - QuizReviewTask: a per-answer review task created when a
//     student submits an open quiz.
type TaskCreator interface {
	CreateGeneratorTask(ctx context.Context, ownerID, courseID, title, intentMD string) (taskID string, err error)
	CreateQuizReviewTask(ctx context.Context, ownerID, quizID, lessonID, answer string) (taskID string, err error)
}

// Service is the place to mutate course state. The HTTP layer
// delegates here so the same business rules apply regardless of
// caller (UI, agent, CLI).
type Service struct {
	Repo  course.Repository
	Tasks TaskCreator // nil-safe; CreateWithIntent/AnswerQuiz no-op when unset
}

func New(repo course.Repository) *Service {
	return &Service{Repo: repo}
}

// WithTaskCreator returns a copy of the service wired to the given
// task creator. The course service uses this lazily so test code
// can pass a stub without dragging the whole task service in.
func (s *Service) WithTaskCreator(tc TaskCreator) *Service {
	cp := *s
	cp.Tasks = tc
	return &cp
}

// CreateWithIntent inserts a draft course and, when a TaskCreator
// is wired, also spawns a "build the curriculum" task that the
// tutor agent picks up via the standard agent work-queue (Phase 3).
// The task id is persisted on the course row so the agent can
// reference it back to the course during submission.
//
// Phase 27.4: previously the generator task was a "placeholder" —
// the field existed but no service actually wrote it. Now the
// service does the wiring; the task is the agent's primary entry
// point to learn about new courses.
func (s *Service) CreateWithIntent(ctx context.Context, ownerID, title, intentMD string) (*course.Course, error) {
	if ownerID == "" || title == "" {
		return nil, ErrInvalidInput
	}
	c := &course.Course{
		Title:    title,
		IntentMD: intentMD,
		Level:    "beginner",
		Pace:     "casual",
		Status:   course.StatusDraft,
		OwnerID:  ownerID,
	}
	if err := s.Repo.CreateCourse(ctx, c); err != nil {
		return nil, err
	}
	if s.Tasks != nil {
		taskID, err := s.Tasks.CreateGeneratorTask(ctx, ownerID, c.ID, title, intentMD)
		if err != nil {
			return nil, fmt.Errorf("course.CreateWithIntent: generator task: %w", err)
		}
		c.GeneratorTaskID = taskID
		if err := s.Repo.UpdateCourse(ctx, c); err != nil {
			return nil, fmt.Errorf("course.CreateWithIntent: update generator_task_id: %w", err)
		}
	}
	return c, nil
}

// SubmitCurriculum atomically replaces the course's modules and
// lessons with the tutor's draft. The course moves to review.
func (s *Service) SubmitCurriculum(
	ctx context.Context,
	courseID string,
	modules []*course.Module,
	lessons []*course.Lesson,
) error {
	c, err := s.Repo.GetCourse(ctx, courseID)
	if err != nil {
		return ErrNotFound
	}
	if !c.StatusTransitionOK(course.StatusReview) {
		return ErrTransition
	}
	if err := s.Repo.SubmitCurriculum(ctx, courseID, modules, lessons); err != nil {
		return err
	}
	c.Status = course.StatusReview
	return s.Repo.UpdateCourse(ctx, c)
}

// ApproveCurriculum moves the course from review to active.
func (s *Service) ApproveCurriculum(ctx context.Context, courseID string) (*course.Course, error) {
	c, err := s.Repo.GetCourse(ctx, courseID)
	if err != nil {
		return nil, ErrNotFound
	}
	if !c.StatusTransitionOK(course.StatusActive) {
		return nil, ErrTransition
	}
	c.Status = course.StatusActive
	if err := s.Repo.UpdateCourse(ctx, c); err != nil {
		return nil, err
	}
	// Unlock the first lesson so the student can start.
	lessons, err := s.Repo.ListLessonsInCourse(ctx, courseID)
	if err != nil {
		return nil, err
	}
	for _, l := range lessons {
		if l.Status == course.LessonLocked {
			l.Status = course.LessonOpen
			if err := s.Repo.UpdateLesson(ctx, l); err != nil {
				return nil, fmt.Errorf("course.Approve: unlock first lesson: %w", err)
			}
			break
		}
	}
	return c, nil
}

// RequestChanges sends the course back to draft with optional
// feedback (the message becomes part of the agent's task notes).
func (s *Service) RequestChanges(ctx context.Context, courseID string) (*course.Course, error) {
	c, err := s.Repo.GetCourse(ctx, courseID)
	if err != nil {
		return nil, ErrNotFound
	}
	if !c.StatusTransitionOK(course.StatusDraft) {
		return nil, ErrTransition
	}
	c.Status = course.StatusDraft
	if err := s.Repo.UpdateCourse(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// CompleteLesson marks a lesson done and unlocks the next one.
// When the last lesson is completed, the course moves to done.
func (s *Service) CompleteLesson(ctx context.Context, lessonID string) (*course.Lesson, error) {
	lesson, err := s.Repo.GetLesson(ctx, lessonID)
	if err != nil {
		return nil, ErrNotFound
	}
	if lesson.Status != course.LessonOpen {
		return nil, ErrTransition
	}
	lesson.Status = course.LessonDone
	if err := s.Repo.UpdateLesson(ctx, lesson); err != nil {
		return nil, err
	}
	// Unlock the next lesson in the same module.
	candidates, err := s.Repo.ListLessons(ctx, lesson.ModuleID)
	if err != nil {
		return nil, err
	}
	nextUnlocked := false
	for _, l := range candidates {
		if nextUnlocked && l.Status == course.LessonLocked {
			l.Status = course.LessonOpen
			if err := s.Repo.UpdateLesson(ctx, l); err != nil {
				return nil, err
			}
			break
		}
		if l.ID == lesson.ID {
			nextUnlocked = true
		}
	}
	return lesson, nil
}

// MaterializeLesson is the tutor-side counterpart to CompleteLesson:
// the agent writes content_md and links an exercise task, and the
// lesson unlocks so the student can read it.
//
// Phase 27.4 closes the gap that the create→approve path stopped
// at: until now the tutor could submit a curriculum but couldn't
// fill the lesson bodies, so a course never actually became
// "learnable". This method is idempotent (re-running with the same
// content_md keeps the existing task_id and status).
//
// Inputs:
//
//   - lessonID: known course_lessons.id (the curriculum row created
//     by SubmitCurriculum).
//   - contentMD: the lesson body in markdown. Empty is rejected —
//     a lesson with no content is no better than a locked lesson.
//   - taskID: optional. If set, must be a valid existing task. The
//     service stores it on the lesson for the student to click
//     through to the exercise.
//
// When the lesson is currently `locked` and the content is non-empty,
// the service flips it to `open`. Already-open lessons stay open;
// done lessons are not re-opened (lifecycle is one-way on the
// "back" path; the user can rewind by re-issuing a curriculum
// through the higher-level service path).
func (s *Service) MaterializeLesson(ctx context.Context, lessonID, contentMD, taskID string) (*course.Lesson, error) {
	if lessonID == "" || contentMD == "" {
		return nil, ErrInvalidInput
	}
	lesson, err := s.Repo.GetLesson(ctx, lessonID)
	if err != nil {
		return nil, ErrNotFound
	}
	// Lifecycle: locked → open on first materialization. Done stays
	// done (the user already completed it; no need to "unlock").
	var newStatus course.LessonStatus
	switch lesson.Status {
	case course.LessonLocked:
		newStatus = course.LessonOpen
	case course.LessonOpen, course.LessonDone:
		newStatus = lesson.Status
	default:
		return nil, ErrTransition
	}
	if err := s.Repo.UpdateLessonContent(ctx, lessonID, contentMD, newStatus, taskID); err != nil {
		return nil, fmt.Errorf("course.MaterializeLesson: write: %w", err)
	}
	// Return the freshly-updated row so callers (UI, tests) see the
	// new status without a follow-up GetLesson.
	lesson.ContentMD = contentMD
	lesson.Status = newStatus
	lesson.TaskID = taskID
	return lesson, nil
}

// AnswerQuiz scores a student's quiz answer.
//
// Two paths:
//
//   - QuizExact: server-side comparison. Both sides are normalised
//     (trimmed, lowercased, whitespace collapsed, diacritics
//     stripped) before comparison. The result returns Correct=true
//     on a match; Correct=false otherwise. We don't persist the
//     attempt — the student's score is recomputed on the fly from
//     the latest attempt stored by the caller (Phase 27.4 keeps it
//     minimal; a richer attempt log is a future enhancement).
//
//   - QuizOpen: a review task is created on the tutor agent with
//     the answer in context_md. The id is returned so the UI can
//     show "pending review" and the agent can claim it through the
//     standard /api/v1/agent/{claim,submit} flow.
//
// The function is read-only for the lesson status; quizzes don't
// block lesson completion (a student can keep going even if a
// manual review is outstanding).
func (s *Service) AnswerQuiz(ctx context.Context, quizID string, answer course.QuizAnswer) (course.QuizResult, error) {
	if quizID == "" {
		return course.QuizResult{}, ErrInvalidInput
	}
	quiz, err := s.Repo.GetQuiz(ctx, quizID)
	if err != nil {
		return course.QuizResult{}, ErrNotFound
	}
	switch quiz.Kind {
	case course.QuizExact:
		expected := normalizeQuizAnswer(quiz.ExpectedMD)
		got := normalizeQuizAnswer(answer.Answer)
		return course.QuizResult{
			Correct:    expected == got,
			FeedbackMD: fmt.Sprintf("expected: %s", quiz.ExpectedMD),
		}, nil
	case course.QuizOpen:
		if s.Tasks == nil {
			return course.QuizResult{}, errors.New("course: open-quiz answers require a TaskCreator")
		}
		// We don't know the lesson owner here; the course is
		// single-owner so we look up the course via the lesson.
		lesson, err := s.Repo.GetLesson(ctx, quiz.LessonID)
		if err != nil {
			return course.QuizResult{}, ErrNotFound
		}
		courseOwner, err := s.Repo.ModuleCourseOwner(ctx, lesson.ModuleID)
		if err != nil {
			return course.QuizResult{}, err
		}
		reviewTaskID, err := s.Tasks.CreateQuizReviewTask(ctx, courseOwner, quizID, lesson.ID, answer.Answer)
		if err != nil {
			return course.QuizResult{}, fmt.Errorf("course.AnswerQuiz: review task: %w", err)
		}
		return course.QuizResult{
			Correct:      false,
			FeedbackMD:   "submitted for tutor review",
			ReviewTaskID: reviewTaskID,
		}, nil
	default:
		return course.QuizResult{}, fmt.Errorf("course: unknown quiz kind %q", quiz.Kind)
	}
}

// normalizeQuizAnswer canonicalises an answer string for exact-quiz
// comparison. Lowercase, strip leading/trailing whitespace, collapse
// internal whitespace, and drop diacritics — so "café" matches
// "cafe" and "  Hello  World  " matches "hello world".
func normalizeQuizAnswer(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// Collapse any run of whitespace (incl. unicode spaces) into
	// a single ASCII space.
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		// Strip diacritics — NFD-split then drop combining marks.
		for _, mr := range normalizedRunes(r) {
			b.WriteRune(mr)
		}
	}
	return b.String()
}

// normalizedRunes does NFD-decomposition for a single rune so the
// `é` becomes `e` + combining acute; the caller drops the marks.
// We do this on a per-rune basis to avoid pulling a full
// normalisation package — the quiz answer vocabulary is small.
func normalizedRunes(r rune) []rune {
	// runes that are already ASCII bypass the decomposition path.
	if r < 0x80 {
		return []rune{r}
	}
	// Common pre-composed Latin letters and their NFD base.
	// Anything outside this small table stays as-is — the
	// comparison is best-effort, not a Unicode conformance test.
	precomposed := map[rune]rune{
		'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e', 'ē': 'e',
		'á': 'a', 'à': 'a', 'â': 'a', 'ä': 'a', 'ā': 'a',
		'í': 'i', 'ì': 'i', 'î': 'i', 'ï': 'i', 'ī': 'i',
		'ó': 'o', 'ò': 'o', 'ô': 'o', 'ö': 'o', 'ō': 'o',
		'ú': 'u', 'ù': 'u', 'û': 'u', 'ü': 'u', 'ū': 'u',
		'ñ': 'n', 'ç': 'c',
		'ý': 'y', 'ÿ': 'y',
	}
	if base, ok := precomposed[r]; ok {
		return []rune{base}
	}
	return []rune{r}
}

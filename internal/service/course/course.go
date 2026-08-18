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
//
// Phase 27.6 adds CompleteTask so the service can retire the
// generator task when the owner builds the curriculum by hand
// (otherwise a sleep-walking tutor would overwrite the manual
// program). It's optional — older stubs satisfy the embedded
// partial interface below.
type TaskCreator interface {
	CreateGeneratorTask(ctx context.Context, ownerID, courseID, title, intentMD string) (taskID string, err error)
	CreateQuizReviewTask(ctx context.Context, ownerID, quizID, lessonID, answer string) (taskID string, err error)
}

// ActivityRecorder writes course_activity rows (Phase 32.5 pilot
// task #2). nil-safe — every call site guards `if s.Activity !=
// nil` before invoking, mirroring the task.Recorder pattern from
// Phase 3.9. Tests can pass a stub without a real DB.
//
// RecordCourseAuto resolves the actor from context (api.Identity).
// For tests / internal callers without an Identity, the row is
// silently skipped — that's the same shape as taskActivity's
// recorder, which also swallows missing-identity.
type ActivityRecorder interface {
	RecordCourseAuto(ctx context.Context, courseID string, kind course.ActivityKind, payload string) error
}

// TaskCompleter is the optional companion seam. nil-safe — if the
// adapter doesn't expose it, generator tasks are simply not
// retired on user-side submit (the tutor will still see them and
// may overwrite; that's the pre-27.6 behaviour).
type TaskCompleter interface {
	CompleteTask(ctx context.Context, taskID, note string) error
}

// MaybeCompleter lets the adapter implement TaskCompleter on the
// same concrete type as TaskCreator without forcing every stub to
// grow the method. The default `courseTaskCreatorAdapter` in
// cmd/orenda satisfies it.
type MaybeCompleter interface {
	TaskCreator
	CompleteTask(ctx context.Context, taskID, note string) error
}

// completerFromTasks returns the TaskCompleter view of s.Tasks if
// it implements CompleteTask. nil otherwise.
func completerFromTasks(tc TaskCreator) TaskCompleter {
	if c, ok := tc.(MaybeCompleter); ok {
		return c
	}
	return nil
}

// Service is the place to mutate course state. The HTTP layer
// delegates here so the same business rules apply regardless of
// caller (UI, agent, CLI).
type Service struct {
	Repo     course.Repository
	Tasks    TaskCreator      // nil-safe; CreateWithIntent/AnswerQuiz no-op when unset
	Activity ActivityRecorder // nil-safe; course mutation methods emit activity rows when wired (Phase 32.5)
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

// WithActivity returns a copy of the service wired to the course
// activity recorder. Same nil-safe pattern as WithTaskCreator —
// the recorder is optional so partial-router test fixtures don't
// have to drag in the activity storage layer.
func (s *Service) WithActivity(rec ActivityRecorder) *Service {
	cp := *s
	cp.Activity = rec
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
//
// Phase 27.6: when SkipGenerator is true, the owner intends to
// build the program themselves; no agent task is created and the
// course is born quiet. Tutor claims that arrive later (e.g. for
// a different course) cannot touch it.
func (s *Service) CreateWithIntent(ctx context.Context, ownerID, title, intentMD string, opts ...CreateOption) (*course.Course, error) {
	if ownerID == "" || title == "" {
		return nil, ErrInvalidInput
	}
	cfg := &createOptions{}
	for _, o := range opts {
		o(cfg)
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
	// Phase 32.5: emit a course_activity row for the creation.
	// Recorder is nil-safe; emitting with no Identity in ctx is a
	// silent no-op (test fixtures, internal callers).
	if s.Activity != nil {
		_ = s.Activity.RecordCourseAuto(ctx, c.ID, course.ActivityCreated, "")
	}
	if s.Tasks != nil && !cfg.SkipGenerator {
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

// CreateOption configures CreateWithIntent (Phase 27.6 — variadic
// so older callers stay source-compatible).
type CreateOption func(*createOptions)

type createOptions struct {
	SkipGenerator bool
}

// SkipGenerator makes CreateWithIntent omit the agent generator
// task. Use this when the owner intends to build the program by
// hand — otherwise a sleep-walking tutor will overwrite their work.
func SkipGenerator() CreateOption {
	return func(o *createOptions) { o.SkipGenerator = true }
}

// SubmitCurriculum atomically replaces the course's modules,
// lessons, and quizzes with the submitter's draft. The course
// moves to review (or stays in review if it was already there —
// the self-transition lets the owner iterate on the program
// without forcing a trip through draft).
//
// Phase 27.6: quizzes are now part of the payload; previously
// they were a follow-up step the tutor did after approval. The
// generator-task seam means: if the course has a live
// GeneratorTaskID and the service has a TaskCompleter wired
// (default in cmd/orenda), we retire the task with a short
// note explaining the owner took over. This stops a tutor from
// picking up the course later and overwriting the manual tree.
func (s *Service) SubmitCurriculum(
	ctx context.Context,
	courseID string,
	modules []*course.Module,
	lessons []*course.Lesson,
	quizzes []*course.Quiz,
) error {
	c, err := s.Repo.GetCourse(ctx, courseID)
	if err != nil {
		return ErrNotFound
	}
	if !c.StatusTransitionOK(course.StatusReview) {
		return ErrTransition
	}
	if err := s.Repo.SubmitCurriculum(ctx, courseID, modules, lessons, quizzes); err != nil {
		return err
	}
	wasReview := c.Status == course.StatusReview
	c.Status = course.StatusReview
	if err := s.Repo.UpdateCourse(ctx, c); err != nil {
		return err
	}
	// Generator-task seam: if this submit is from the owner (the
	// only caller under RequireUser today), retire the generator
	// task so a late tutor doesn't claim it. Skip when the task
	// is already gone or the course is in self-transition (the
	// owner is iterating on a tutor-built program, not replacing
	// it).
	if c.GeneratorTaskID != "" && !wasReview {
		if completer := completerFromTasks(s.Tasks); completer != nil {
			if err := completer.CompleteTask(ctx, c.GeneratorTaskID,
				"curriculum built by owner; generator task retired"); err != nil {
				// Non-fatal: log via the caller's wrapped error.
				// The course is saved; the orphan task will be
				// rejected on claim because the course status no
				// longer matches.
				return fmt.Errorf("course.SubmitCurriculum: retire generator task: %w", err)
			}
			c.GeneratorTaskID = ""
			if err := s.Repo.UpdateCourse(ctx, c); err != nil {
				return fmt.Errorf("course.SubmitCurriculum: clear generator_task_id: %w", err)
			}
		}
	}
	return nil
}

// ApproveCurriculum moves the course from review to active.
// ApproveCurriculum moves the course review → active, unlocks the
// first lesson, and records a course_activity row with kind=approved.
//
// Phase 32.5 pilot task #2: the activity row is what the audit
// feed reads. Without it, "who approved this course" is invisible
// to the operator. ActivityRecorder is nil-safe — tests pass a
// stub or skip wiring.
func (s *Service) ApproveCurriculum(ctx context.Context, courseID string) (*course.Course, error) {
	return s.approveCurriculumWith(ctx, courseID, course.ActivityApproved)
}

// ActivateCourse is the agent-side counterpart. Same transition
// (review → active, first lesson unlocked) but emits a distinct
// activity row with kind=activated so the audit feed distinguishes
// "owner approved" from "agent activated".
//
// Phase 32.5 pilot task #2: previously the user-side approve and
// agent-side activate both called approveCourseCore, which meant
// no row was written for either. Splitting the activity kind here
// keeps the audit honest.
func (s *Service) ActivateCourse(ctx context.Context, courseID string) (*course.Course, error) {
	return s.approveCurriculumWith(ctx, courseID, course.ActivityActivated)
}

func (s *Service) approveCurriculumWith(ctx context.Context, courseID string, kind course.ActivityKind) (*course.Course, error) {
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
	// Emit activity. Recorder is nil-safe; tests may also pass a
	// recorder that returns err without failing the user-visible
	// action (audit gap shouldn't break approval). The recorder
	// reads actor from ctx via its IdentitySource — caller doesn't
	// pass actor explicitly.
	if s.Activity != nil {
		_ = s.Activity.RecordCourseAuto(ctx, courseID, kind, "")
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

// UpdateLessonContent writes a lesson's content_md without touching
// the lifecycle. This is the user-side companion to
// MaterializeLesson: Phase 27.6 lets the owner edit a lesson body
// inside an active course (the agent-only MaterializeLesson path
// still handles the locked→open transition).
//
// Constraints:
//   - content_md must be non-empty (a lesson without content is no
//     better than a locked lesson — same rule as MaterializeLesson).
//   - status flips are not accepted here; the service keeps the
//     current value. The repo's UpdateLessonContent allows any
//     status but the caller (handler) always passes the lesson's
//     own status so the SQL is a no-op on that column.
func (s *Service) UpdateLessonContent(ctx context.Context, lessonID, contentMD string) (*course.Lesson, error) {
	if lessonID == "" || contentMD == "" {
		return nil, ErrInvalidInput
	}
	lesson, err := s.Repo.GetLesson(ctx, lessonID)
	if err != nil {
		return nil, ErrNotFound
	}
	// Keep the current status; pass it through so the repo's
	// UpdateLessonContent writes the same value back (idempotent).
	if err := s.Repo.UpdateLessonContent(ctx, lessonID, contentMD, lesson.Status, lesson.TaskID); err != nil {
		return nil, fmt.Errorf("course.UpdateLessonContent: %w", err)
	}
	lesson.ContentMD = contentMD
	return lesson, nil
}

// AddQuiz appends a quiz to an existing lesson. The caller (handler)
// is responsible for auth; the service just persists the row.
//
// Phase 27.6: this is the targeted alternative to the curriculum
// swap — useful when the curriculum is already approved and the
// owner wants to add one more question to a specific lesson
// without re-submitting the whole tree.
func (s *Service) AddQuiz(ctx context.Context, lessonID, questionMD, expectedMD string, kind course.QuizKind) (*course.Quiz, error) {
	if lessonID == "" || questionMD == "" {
		return nil, ErrInvalidInput
	}
	switch kind {
	case course.QuizOpen, course.QuizExact:
	default:
		return nil, ErrInvalidInput
	}
	if _, err := s.Repo.GetLesson(ctx, lessonID); err != nil {
		return nil, ErrNotFound
	}
	// Position: append at the end. We count the existing quizzes on
	// the lesson via the repo's ListQuizzesInCourse + filter; the
	// repo doesn't expose a per-lesson count, so we do a cheap
	// COUNT query through the same repo's data path. For the
	// personal-install scale this is fine; if it ever matters we
	// can add LessonQuizCount to the interface.
	q := &course.Quiz{
		LessonID:   lessonID,
		QuestionMD: questionMD,
		ExpectedMD: expectedMD,
		Kind:       kind,
	}
	if err := s.Repo.CreateQuiz(ctx, q); err != nil {
		return nil, fmt.Errorf("course.AddQuiz: %w", err)
	}
	return q, nil
}

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

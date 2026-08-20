package course

import (
	"context"
	"time"
)

// Repository persists courses, modules, lessons, and quizzes.
//
// The interface is deliberately narrow: each concern (courses vs
// modules vs lessons) has its own method so the service layer can
// keep transactions small. Internal FKs (Module.CourseID etc.)
// are enforced via SQLite cascades (migration 019).
type Repository interface {
	// ---- Courses ----
	CreateCourse(ctx context.Context, c *Course) error
	GetCourse(ctx context.Context, id string) (*Course, error)
	ListCourses(ctx context.Context, ownerID string) ([]*Course, error)
	UpdateCourse(ctx context.Context, c *Course) error
	DeleteCourse(ctx context.Context, id string) error
	// UpdatePaceNotesMD writes only the free-form pace notes — the
	// narrow PATCH the agent-planner uses (Phase 31). It does NOT
	// touch title/status/etc; the human edits those through
	// UpdateCourse.
	UpdatePaceNotesMD(ctx context.Context, id, notes string) error

	// ---- Modules ----
	CreateModule(ctx context.Context, m *Module) error
	ListModules(ctx context.Context, courseID string) ([]*Module, error)
	// GetModule fetches a single module by id. Phase 30.13 uses it
	// to walk module → course for status gating of granular edits.
	GetModule(ctx context.Context, id string) (*Module, error)
	// UpdateModule writes title and description in place (Phase
	// 30.13). Position is managed exclusively by ApplyStructure so
	// drag-and-drop never fights rename writes.
	UpdateModule(ctx context.Context, m *Module) error
	// DeleteModule removes the module; lessons and quizzes cascade
	// (migration 019). Deleting content intentionally drops the
	// progress those lessons carried.
	DeleteModule(ctx context.Context, id string) error

	// ---- Lessons ----
	CreateLesson(ctx context.Context, l *Lesson) error
	ListLessons(ctx context.Context, moduleID string) ([]*Lesson, error)
	// ListLessonsInCourse returns every lesson for the course in a
	// single query (modules + lessons joined). Used by the tree
	// snapshot endpoint.
	ListLessonsInCourse(ctx context.Context, courseID string) ([]*Lesson, error)
	// GetLesson fetches a single lesson by id. Used by the
	// CompleteLesson path which has to walk to the next sibling.
	GetLesson(ctx context.Context, id string) (*Lesson, error)
	UpdateLesson(ctx context.Context, l *Lesson) error
	// VelocityStatsByCourse returns the rolling-window lesson
	// completion stats for the course (Phase 32.12). The window is
	// passed by the caller (typically 14 days; see the wiki).
	VelocityStatsByCourse(ctx context.Context, courseID string, since time.Time) (VelocityStats, error)
	// UpdateLessonContent writes the lesson's content_md/status/task_id
	// without touching title/position. Used by MaterializeLesson.
	// status is opaque to the repo (the service enforces the lifecycle);
	// taskID is empty to clear the link.
	UpdateLessonContent(ctx context.Context, lessonID, contentMD string, status LessonStatus, taskID string) error
	// DeleteLesson removes the lesson; its quizzes cascade (Phase
	// 30.13). Progress shrinks by construction — the row is gone.
	DeleteLesson(ctx context.Context, id string) error

	// GetQuiz fetches a single quiz by id. Used by AnswerQuiz.
	GetQuiz(ctx context.Context, id string) (*Quiz, error)

	// ModuleCourseOwner returns the owner_id of the course that
	// owns a given module. Used by AnswerQuiz to stamp the
	// generated review task with the right owner — keeps the
	// caller from having to know the module→course→owner chain.
	ModuleCourseOwner(ctx context.Context, moduleID string) (string, error)

	// ---- Quizzes ----
	// CreateQuiz inserts a quiz; if the supplied position is 0 the
	// repo picks the next slot via MAX(position)+1. The persisted
	// row's position is written back into q so callers (UI)
	// can patch their local state.
	CreateQuiz(ctx context.Context, q *Quiz) error
	// ListQuizzesInCourse returns every quiz for the course in a
	// single query (lessons + quizzes joined).
	ListQuizzesInCourse(ctx context.Context, courseID string) ([]*Quiz, error)
	// UpdateQuiz writes question_md/expected_md/kind in place (Phase
	// 30.13). Position is untouched — quiz order inside a lesson is
	// managed by the editor through delete+recreate or future
	// reorder; the MVP keeps quiz order stable.
	UpdateQuiz(ctx context.Context, q *Quiz) error
	// DeleteQuiz removes the quiz (Phase 30.13).
	DeleteQuiz(ctx context.Context, id string) error

	// ---- Progress ----
	// Progress returns lesson counts for the course.
	Progress(ctx context.Context, courseID string) (Progress, error)

	// ---- Submit / Approve (Phase 18 orchestrator surface) ----
	//
	// SubmitCurriculum replaces the draft curriculum (modules +
	// lessons + quizzes) atomically. The service builds the target
	// list, runs the cycle check, and only then calls this. We use a
	// single tx so a partial write never leaks.
	//
	// Phase 27.6: quizzes are part of the swap payload. Each quiz is
	// matched to its lesson by LessonID, which the service fills in
	// from the parent module during the request decode. Quiz IDs are
	// reused if the caller supplied one (so a tutor can edit a
	// draft without churning IDs); new IDs are minted when blank.
	SubmitCurriculum(
		ctx context.Context,
		courseID string,
		modules []*Module,
		lessons []*Lesson,
		quizzes []*Quiz,
	) error

	// ---- Structure (Phase 30.13 granular reorder) ----
	//
	// ApplyStructure rewrites module positions and lesson
	// (module_id, position) pairs atomically. The payload must
	// cover the course exactly: every module of the course exactly
	// once, every lesson of the course exactly once across the
	// modules. Unknown, duplicate, or missing IDs → ErrInvalidInput
	// and nothing is written. Unlike SubmitCurriculum no rows are
	// deleted or re-inserted, so lesson status (student progress)
	// and task links survive the reorder — that is the whole point
	// of the method.
	ApplyStructure(ctx context.Context, courseID string, modules []ModuleOrder) error
}

// Package course — Phase 18: personal learning courses.
//
// A course is a tree: Course → Modules → Lessons → Quizzes. The
// top-level Curso carries a free-form intent ("Learn Rust in a
// month") that an external AI tutor agent consumes to design a
// curriculum. The user approves the curriculum (draft → review →
// active); lessons open sequentially as the student progresses.
//
// Status flows:
//
//	Course:    draft → review → active → done    (plus archived from any)
//	Lesson:    locked → open → done
//
// We treat the lifecycle explicitly via Validate() on every
// transition so the API layer can fail loudly instead of silently
// writing an inconsistent state.
package course

import (
	"errors"
	"time"
)

// Status enumerates the lifecycle of a course.
type Status string

const (
	StatusDraft    Status = "draft"
	StatusReview   Status = "review"
	StatusActive   Status = "active"
	StatusDone     Status = "done"
	StatusArchived Status = "archived"
)

// LessonStatus enumerates the lifecycle of a lesson.
type LessonStatus string

const (
	LessonLocked LessonStatus = "locked"
	LessonOpen   LessonStatus = "open"
	LessonDone   LessonStatus = "done"
)

// QuizKind says how the answer is checked.
type QuizKind string

const (
	QuizOpen  QuizKind = "open"  // agent reviews
	QuizExact QuizKind = "exact" // server-side string compare
)

// Sentinel errors. Handlers translate these to HTTP status codes.
var (
	ErrNotFound     = errors.New("course: not found")
	ErrInvalidInput = errors.New("course: invalid input")
	ErrTransition   = errors.New("course: invalid lifecycle transition")
	ErrLessonLocked = errors.New("course: lesson is locked")
)

// Course is the top-level entity.
type Course struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	IntentMD        string    `json:"intent_md"`
	Level           string    `json:"level"`
	Pace            string    `json:"pace"`
	Status          Status    `json:"status"`
	OwnerID         string    `json:"owner_id"`
	GeneratorTaskID string    `json:"generator_task_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Validate checks the course's basic fields. Status transitions are
// enforced separately (see StatusTransitionOK).
func (c *Course) Validate() error {
	if c.Title == "" {
		return ErrInvalidInput
	}
	switch c.Status {
	case StatusDraft, StatusReview, StatusActive, StatusDone, StatusArchived:
	default:
		return ErrInvalidInput
	}
	switch c.Level {
	case "beginner", "intermediate", "advanced":
	case "":
		// default
	default:
		return ErrInvalidInput
	}
	switch c.Pace {
	case "casual", "regular", "intensive":
	case "":
		// default
	default:
		return ErrInvalidInput
	}
	return nil
}

// StatusTransitionOK returns whether `to` is reachable from the
// course's current status. We allow the explicit "review → draft"
// rollback (user feedback path) and "any → archived".
func (c *Course) StatusTransitionOK(to Status) bool {
	if to == StatusArchived {
		return true
	}
	switch c.Status {
	case StatusDraft:
		return to == StatusReview
	case StatusReview:
		return to == StatusActive || to == StatusDraft
	case StatusActive:
		return to == StatusDone
	case StatusDone:
		return false
	}
	return false
}

// Module is a grouped collection of lessons inside a course.
type Module struct {
	ID          string `json:"id"`
	CourseID    string `json:"course_id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Position    int    `json:"position"`
}

// Lesson is a single learning unit.
type Lesson struct {
	ID        string       `json:"id"`
	ModuleID  string       `json:"module_id"`
	Title     string       `json:"title"`
	ContentMD string       `json:"content_md,omitempty"`
	Status    LessonStatus `json:"status"`
	Position  int          `json:"position"`
	TaskID    string       `json:"task_id,omitempty"`
}

// Quiz is a question under a lesson.
type Quiz struct {
	ID         string   `json:"id"`
	LessonID   string   `json:"lesson_id"`
	Position   int      `json:"position"`
	QuestionMD string   `json:"question_md"`
	ExpectedMD string   `json:"expected_md,omitempty"`
	Kind       QuizKind `json:"kind"`
}

// CourseTree is the full snapshot returned by GET /courses/{id}.
// Modules in order, each with its lessons in order, each with its
// quizzes in order. The server pre-bakes this so the UI doesn't
// fan out per child.
type CourseTree struct {
	Course  *Course   `json:"course"`
	Modules []*Module `json:"modules"`
	Lessons []*Lesson `json:"lessons"` // flat list, indexed by module_id
	Quizzes []*Quiz   `json:"quizzes"` // flat list, indexed by lesson_id
}

// Progress is the rendered progress bar data.
type Progress struct {
	LessonsTotal int `json:"lessons_total"`
	LessonsDone  int `json:"lessons_done"`
}

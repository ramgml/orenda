// Package course — Phase 32.5 pilot task #2: course activity feed.
//
// Mirrors task_activity but scoped to courses. Captures who did
// what (create / approve / activate / granular curriculum CRUD /
// status change / archive) and when, so an operator can reconstruct
// a course's history without diving into the markdown mirror.
package course

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ActivityKind enumerates the mutations we audit for a course.
// New kinds must be added at the end so existing rows still parse.
type ActivityKind string

const (
	ActivityCreated           ActivityKind = "created"
	ActivityApproved          ActivityKind = "approved"  // user approve (draft → review → active path)
	ActivityActivated         ActivityKind = "activated" // agent activate (review → active)
	ActivityCurriculumSwapped ActivityKind = "curriculum_swapped"
	ActivityLessonAdded       ActivityKind = "lesson_added"
	ActivityLessonRemoved     ActivityKind = "lesson_removed"
	ActivityLessonEdited      ActivityKind = "lesson_edited"
	ActivityQuizAdded         ActivityKind = "quiz_added"
	ActivityQuizRemoved       ActivityKind = "quiz_removed"
	ActivityQuizEdited        ActivityKind = "quiz_edited"
	ActivityModuleAdded       ActivityKind = "module_added"
	ActivityModuleRemoved     ActivityKind = "module_removed"
	ActivityModuleEdited      ActivityKind = "module_edited"
	ActivityStatusChanged     ActivityKind = "status_changed"
	ActivityArchived          ActivityKind = "archived"
)

// IsValid returns true for the closed set above.
func (k ActivityKind) IsValid() bool {
	switch k {
	case ActivityCreated, ActivityApproved, ActivityActivated,
		ActivityCurriculumSwapped,
		ActivityLessonAdded, ActivityLessonRemoved, ActivityLessonEdited,
		ActivityQuizAdded, ActivityQuizRemoved, ActivityQuizEdited,
		ActivityModuleAdded, ActivityModuleRemoved, ActivityModuleEdited,
		ActivityStatusChanged, ActivityArchived:
		return true
	}
	return false
}

// ActorType enumerates who caused an activity row. Same shape as
// task_activity (Phase 3.9) so the audit feeds look uniform.
type ActorType string

const (
	ActorUser  ActorType = "user"
	ActorAgent ActorType = "agent"
)

// IsValid for ActorType — closed set above.
func (a ActorType) IsValid() bool {
	return a == ActorUser || a == ActorAgent
}

// Activity is one audit row. One row per course mutation; the
// payload field is a free-form small string (e.g. "lesson=l-123"),
// kept narrow on purpose so the markdown mirror stays readable.
type Activity struct {
	ID        string       `json:"id"`
	CourseID  string       `json:"course_id"`
	ActorType ActorType    `json:"actor_type"`
	ActorID   string       `json:"actor_id"`
	Kind      ActivityKind `json:"kind"`
	Payload   string       `json:"payload,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

// Validate enforces shape invariants. CourseID, ActorID, ID must be
// non-empty (UUIDv7 strings); Kind and ActorType must be from the
// closed sets.
func (a *Activity) Validate() error {
	if a == nil {
		return errors.New("course activity: nil")
	}
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("course activity: id required")
	}
	if strings.TrimSpace(a.CourseID) == "" {
		return errors.New("course activity: course_id required")
	}
	if strings.TrimSpace(a.ActorID) == "" {
		return errors.New("course activity: actor_id required")
	}
	if !a.ActorType.IsValid() {
		return fmt.Errorf("course activity: actor_type %q invalid", a.ActorType)
	}
	if !a.Kind.IsValid() {
		return fmt.Errorf("course activity: kind %q invalid", a.Kind)
	}
	return nil
}

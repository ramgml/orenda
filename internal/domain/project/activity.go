// Package project — project activity feed (wiki:agent-project-description).
//
// Mirrors course/activity.go (migration 023) and task_activity (Phase
// 3.9) but scoped to projects. The audit log records who changed what
// on a project row (description in v1; future kinds land in
// alphabetical order at the end of the closed set) so an operator
// can reconstruct a project's edit history without scraping the
// markdown mirror or relying on backup snapshots.
package project

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ActivityKind enumerates the project mutations we audit. New kinds
// must be added at the end so existing rows still parse.
type ActivityKind string

const (
	// ActivityDescriptionChanged is recorded when an agent PATCHes
	// a project's description (the only v1 case; the user-cookie
	// PATCH is unchanged and writes no project_activity row in v1
	// per the wiki scope note).
	ActivityDescriptionChanged ActivityKind = "description_changed"
	// ActivityWikiSlugChanged is recorded when an agent PATCHes the
	// project's wiki_slug (wiki:project-wiki-link). The user-cookie
	// PATCH writes the same row too — both surfaces are read by the
	// same audit feed and the diff is symmetric.
	ActivityWikiSlugChanged ActivityKind = "wiki_slug_changed"
)

// IsValid reports whether k belongs to the closed set above.
func (k ActivityKind) IsValid() bool {
	return k == ActivityDescriptionChanged || k == ActivityWikiSlugChanged
}

// ActorType enumerates who caused an activity row. Same shape as
// task_activity (Phase 3.9) and course_activity (migration 023) so
// the audit feeds look uniform.
type ActorType string

const (
	ActorUser  ActorType = "user"
	ActorAgent ActorType = "agent"
)

// IsValid for ActorType — closed set above.
func (a ActorType) IsValid() bool {
	return a == ActorUser || a == ActorAgent
}

// Activity is one audit row. One row per project mutation; the
// payload field is a free-form small JSON string kept narrow on
// purpose so the diff stays readable in the markdown mirror.
type Activity struct {
	ID        string       `json:"id"`
	ProjectID string       `json:"project_id"`
	ActorType ActorType    `json:"actor_type"`
	ActorID   string       `json:"actor_id"`
	Kind      ActivityKind `json:"kind"`
	Payload   string       `json:"payload,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

// Validate enforces shape invariants. ProjectID, ActorID, ID must be
// non-empty (UUIDv7 strings); Kind and ActorType must be from the
// closed sets.
func (a *Activity) Validate() error {
	if a == nil {
		return errors.New("project activity: nil")
	}
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("project activity: id required")
	}
	if strings.TrimSpace(a.ProjectID) == "" {
		return errors.New("project activity: project_id required")
	}
	if strings.TrimSpace(a.ActorID) == "" {
		return errors.New("project activity: actor_id required")
	}
	if !a.ActorType.IsValid() {
		return fmt.Errorf("project activity: actor_type %q invalid", a.ActorType)
	}
	if !a.Kind.IsValid() {
		return fmt.Errorf("project activity: kind %q invalid", a.Kind)
	}
	return nil
}

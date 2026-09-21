package activity

import (
	"context"

	"github.com/ramgml/orenda/internal/domain/timeentry"
)

// Repository persists Activity rows.
//
// task_activity is append-only — there is no Update or Delete.
type Repository interface {
	Create(ctx context.Context, a *Activity) error
	ListByTask(ctx context.Context, taskID string) ([]*Activity, error)
	ListByActor(ctx context.Context, actorType ActorType, actorID string) ([]*Activity, error)
	// ListByProject aggregates activity rows from every task that
	// belongs to the given project, newest first. The ProjectActivityEvent
	// carries the joined task title so the project Activity tab can show
	// "X commented on Y" without a second round-trip per row.
	ListByProject(ctx context.Context, projectID string, limit int) ([]*ProjectActivityEvent, error)

	// StatusChangesByTasks returns the `task.status_changed` audit
	// rows for the requested tasks in one round-trip (T356 spent
	// fallback), oldest first per task. A nil taskIDs slice means
	// "every task that has usable rows"; an empty (non-nil) slice
	// returns an empty map. Rows whose payload fails to parse as
	// {"from":…,"to":…} are skipped; tasks with no usable rows are
	// absent from the map. Used only by the time-fallback read path;
	// write paths are untouched.
	StatusChangesByTasks(ctx context.Context, taskIDs []string) (map[string][]timeentry.StatusSpentEvent, error)
}

// ProjectActivityEvent is one row in the project Activity tab.
//
// It carries the underlying Activity plus a TaskTitle so the UI can
// render the event in context without joining tasks client-side. Limit
// is applied at the SQL layer; pass 0 to use a sensible default.
type ProjectActivityEvent struct {
	Activity
	TaskTitle string `json:"task_title"`
}

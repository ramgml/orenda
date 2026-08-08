package activity

import "context"

// Repository persists Activity rows.
//
// task_activity is append-only — there is no Update or Delete.
type Repository interface {
	Create(ctx context.Context, a *Activity) error
	ListByTask(ctx context.Context, taskID string) ([]*Activity, error)
	ListByActor(ctx context.Context, actorType ActorType, actorID string) ([]*Activity, error)
}

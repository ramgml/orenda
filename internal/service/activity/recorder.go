// Package activity provides the Recorder implementation that taskSvc and
// agentSvc use to write audit rows.
//
// Phase 3.9 ships ActivityRecorder which adapts to the task.Recorder and
// agent.Recorder interfaces defined in the service layer.
package activity

import (
	"context"
	"fmt"

	"github.com/ramgml/orenda/internal/domain/activity"
)

// Repository is the small surface the recorder needs.
type Repository interface {
	Create(ctx context.Context, a *activity.Activity) error
}

// Recorder writes activity rows.
type Recorder struct {
	Repo Repository
}

// New returns a Recorder.
func New(repo Repository) *Recorder {
	return &Recorder{Repo: repo}
}

// RecordTask writes a task_activity row. Implements taskSvc.Recorder.
func (r *Recorder) RecordTask(ctx context.Context, taskID string, actorType activity.ActorType, actorID string, action activity.Action, payload string) error {
	a := &activity.Activity{
		TaskID:    taskID,
		ActorType: actorType,
		ActorID:   actorID,
		Action:    action,
		Payload:   payload,
	}
	if err := a.Validate(); err != nil {
		return err
	}
	if err := r.Repo.Create(ctx, a); err != nil {
		return fmt.Errorf("activity.RecordTask: %w", err)
	}
	return nil
}

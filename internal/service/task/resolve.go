package task

import (
	"context"

	"github.com/ramgml/orenda/internal/domain/task"
)

// Resolve returns the task identified by ref, where ref may be a task
// UUID or a T-prefixed number ("T42" / "t42").
//
// This is the seam every task-id-taking entry point should funnel
// through (agent REST /agent/tasks/{id}/*, the `orenda agent` CLI and
// the MCP tools — the latter two ride on the REST surface — plus the
// trivial user-REST lookups) so the "T<N>" convention resolves
// identically everywhere. Unknown T-refs surface as
// *task.RefNotFoundError ("task T42 not found"); unknown ids as
// task.ErrNotFound. Both match ErrNotFound via errors.Is.
func (s *Service) Resolve(ctx context.Context, ref string) (*task.Task, error) {
	return task.ResolveRef(ctx, s.Tasks, ref)
}

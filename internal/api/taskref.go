// Package api — task reference resolution ("#42" / "42" / UUID).
//
// Every handler that takes a task id from the URL and is part of the
// agent surface (REST /api/v1/agent/tasks/{id}/*, which the CLI and
// the MCP tools ride on) resolves the path parameter through
// resolveTaskRef first: pure-digit tokens (with an optional leading
// '#') go through tasks.number, anything else is treated as the task
// UUID. The trivial user-side lookups use the same helper.
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/ramgml/orenda/internal/domain/task"
)

// resolveTaskRef resolves a task reference from the URL to the task's
// UUID. "#42" / "42" resolve through tasks.number; anything else is a
// UUID lookup. Unknown numeric refs surface as *task.RefNotFoundError
// ("task #N not found"); unknown ids as task.ErrNotFound.
func resolveTaskRef(ctx context.Context, deps *Dependencies, ref string) (string, error) {
	tr, err := task.ResolveRef(ctx, deps.Tasks, ref)
	if err != nil {
		return "", err
	}
	return tr.ID, nil
}

// writeResolveError translates a resolveTaskRef failure: 404 with the
// explicit "task #N not found" message for numeric refs (the agent
// needs to see WHICH number didn't resolve), the generic not_found
// body otherwise. Non-not-found errors fall through to writeError.
func writeResolveError(w http.ResponseWriter, err error) {
	var refErr *task.RefNotFoundError
	if errors.As(err, &refErr) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": refErr.Error()})
		return
	}
	if errors.Is(err, task.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	writeError(w, err)
}

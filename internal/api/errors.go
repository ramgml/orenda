// Package api — common error translation helpers.
package api

import (
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/domain/wiki"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	wikiservice "github.com/ramgml/orenda/internal/service/wiki"
)

// apiLogger is the package-level logger used by writeError for unexpected
// (500) errors. It is set once during router construction. nil is fine —
// writeError just skips the log entry. Tests can override it via
// SetAPILogger to surface internal errors.
var apiLogger *zap.Logger

// SetAPILogger installs the logger used by writeError for 500-class
// errors. Pass nil to disable logging.
func SetAPILogger(l *zap.Logger) { apiLogger = l }

// writeError translates a domain error into the appropriate HTTP status code
// and writes a small JSON body. Unknown errors become 500.
//
// Use this from every handler so the wire format stays consistent.
//
// Phase 30.7 added a service-side validation that rejects a review
// without a comment (decision=reject && trim(comment) == ""), but
// the original switch only mapped domain-package sentinels
// (task.ErrInvalidInput, etc.). Service packages — task/taskservice —
// have their own ErrInvalidInput that wasn't covered, so the path
// rendered 500 {"error":"internal"} instead of 400. Phase 30.17
// completes the mapping: every ErrInvalidInput from any layer
// resolves to 400 invalid_input.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, project.ErrColumnNotEmpty):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "column_not_empty"})
	case errors.Is(err, user.ErrNotFound),
		errors.Is(err, project.ErrNotFound),
		errors.Is(err, task.ErrNotFound),
		errors.Is(err, wiki.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	case errors.Is(err, user.ErrEmailTaken),
		errors.Is(err, wiki.ErrSlugTaken),
		errors.Is(err, wikiservice.ErrSlugTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "slug_taken"})
	case errors.Is(err, user.ErrInvalidInput),
		errors.Is(err, project.ErrInvalidInput),
		errors.Is(err, task.ErrInvalidInput),
		errors.Is(err, taskservice.ErrInvalidInput),
		errors.Is(err, wiki.ErrInvalidInput),
		errors.Is(err, wikiservice.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
	default:
		if apiLogger != nil {
			apiLogger.Error("api internal error", zap.Error(err))
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
	}
}

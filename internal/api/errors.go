// Package api — common error translation helpers.
package api

import (
	"errors"
	"net/http"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
)

// writeError translates a domain error into the appropriate HTTP status code
// and writes a small JSON body. Unknown errors become 500.
//
// Use this from every handler so the wire format stays consistent.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, user.ErrNotFound),
		errors.Is(err, project.ErrNotFound),
		errors.Is(err, task.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	case errors.Is(err, user.ErrEmailTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "email_taken"})
	case errors.Is(err, user.ErrInvalidInput),
		errors.Is(err, project.ErrInvalidInput),
		errors.Is(err, task.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
	}
}

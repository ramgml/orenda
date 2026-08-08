// Package api — project CRUD handlers.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/project"
)

// projectInput is the JSON body for create/update operations.
type projectInput struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
	Archived    bool   `json:"archived"`
}

// listProjectsHandler returns the authenticated user's projects.
func listProjectsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := IdentityFrom(r.Context())
		if id == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no_identity"})
			return
		}
		projects, err := deps.Projects.ListProjects(r.Context(), id.UserID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
	}
}

// createProjectHandler creates a new project owned by the caller.
func createProjectHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := IdentityFrom(r.Context())
		if id == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no_identity"})
			return
		}
		var in projectInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		p := &project.Project{
			Name:        in.Name,
			Color:       in.Color,
			Description: in.Description,
			Archived:    in.Archived,
			OwnerID:     id.UserID,
		}
		created, _, _, err := deps.Projects.CreateProject(r.Context(), p)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

// getProjectHandler returns one project.
func getProjectHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := deps.Projects.GetProject(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// patchProjectHandler updates mutable project fields.
func patchProjectHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := deps.Projects.GetProject(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		var in projectInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Name != "" {
			p.Name = in.Name
		}
		if in.Color != "" {
			p.Color = in.Color
		}
		if in.Description != "" {
			p.Description = in.Description
		}
		p.Archived = in.Archived
		if err := deps.Projects.UpdateProject(r.Context(), p); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// deleteProjectHandler removes a project.
func deleteProjectHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := deps.Projects.DeleteProject(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// getProjectBoardHandler returns the (single) board + its columns.
func getProjectBoardHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		board, cols, err := deps.Projects.GetBoard(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"board":   board,
			"columns": cols,
		})
	}
}

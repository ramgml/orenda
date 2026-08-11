// Package api — project-scoped handlers beyond basic CRUD.
//
// Phase 11 splits the project page into tabs (Kanban, Activity,
// Attachments, Settings). These handlers back the Activity and
// Attachments tabs without duplicating the task-level handlers in
// internal/api/handlers_phase3.go — they reuse the same underlying
// services via the polymorphic TargetType ("project").
package api

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/attachment"
)

// listProjectActivityHandler returns the merged activity feed for a
// project — every action taken on any of its tasks, newest first.
//
// The optional `?limit=N` query parameter clamps the response size; the
// repository caps at 200 by default and 500 maximum. This keeps the
// payload predictable for projects with thousands of events.
func listProjectActivityHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Activities == nil {
			http.Error(w, "activity service not wired", http.StatusServiceUnavailable)
			return
		}
		projectID := chi.URLParam(r, "id")
		limit := 200
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		}
		events, err := deps.Activities.ListByProject(r.Context(), projectID, limit)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"activity": events})
	}
}

// listProjectAttachmentsHandler returns every attachment that belongs
// to a project: rows attached directly to the project AND rows
// attached to any of its tasks. Each row carries its target_type so
// the UI can label "From task Foo" vs "Project attachment", and
// task attachments carry the task's title via task_title.
//
// This is the single feed the project attachments tab reads — it
// replaces the earlier "project-only" listing so users can find any
// file uploaded against anything in the project from one place.
func listProjectAttachmentsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Attachments == nil {
			http.Error(w, "attachment service not wired", http.StatusServiceUnavailable)
			return
		}
		projectID := chi.URLParam(r, "id")
		got, err := deps.Attachments.ListByProject(r.Context(), projectID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"attachments": got})
	}
}

// addProjectAttachmentHandler accepts multipart/form-data with a
// `file` field and stores it against the project. Behaviour mirrors
// addTaskAttachmentHandler; only the TargetType differs.
//
// Note: the global /api/v1/attachments/{attId}/download route in
// router.go is reused for downloads — Open() resolves the target_type
// from the row, so no project-specific download handler is needed.
func addProjectAttachmentHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Attachments == nil {
			http.Error(w, "attachment service not wired", http.StatusServiceUnavailable)
			return
		}
		const maxMem = 32 << 20
		if err := r.ParseMultipartForm(maxMem); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_multipart"})
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_file"})
			return
		}
		defer file.Close()

		mimeType := header.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		filename := filepath.Base(header.Filename)
		if filename == "." || filename == "/" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_filename"})
			return
		}

		uploaderID := ""
		if id, ok := IdentityFrom(r.Context()); ok {
			uploaderID = id.UserID
		}

		res, err := deps.Attachments.StoreFromBytes(
			r.Context(),
			attachment.TargetProject,
			chi.URLParam(r, "id"),
			filename, mimeType,
			attachment.UploaderUser, uploaderID,
			file,
		)
		if err != nil {
			if strings.Contains(err.Error(), "too large") {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_large"})
				return
			}
			if strings.Contains(err.Error(), "mime type not allowed") {
				writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "mime_rejected"})
				return
			}
			writeError(w, err)
			return
		}
		if res.Duplicate {
			w.Header().Set("X-Attachment-Duplicate", "true")
		}
		writeJSON(w, http.StatusCreated, res.Attachment)
	}
}

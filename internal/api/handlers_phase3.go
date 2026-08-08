// Package api — Phase 3 handlers: task action endpoints + comments,
// attachments, activity, and the agent-facing /context snapshot.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/attachment"
	"github.com/ramgml/orenda/internal/domain/comment"
	taskservice "github.com/ramgml/orenda/internal/service/task"
)

// claimRequest is the body of POST /tasks/:id/claim.
type claimRequest struct {
	AgentID string `json:"agent_id"`
}

func claimTaskHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.TaskService == nil {
			http.Error(w, "task service not wired", http.StatusServiceUnavailable)
			return
		}
		var req claimRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		tr, err := deps.TaskService.Claim(r.Context(), chi.URLParam(r, "id"), req.AgentID)
		if err != nil {
			if errors.Is(err, taskservice.ErrLockTaken) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "lock_taken"})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tr)
	}
}

func releaseTaskHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.TaskService == nil {
			http.Error(w, "task service not wired", http.StatusServiceUnavailable)
			return
		}
		var req claimRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		tr, err := deps.TaskService.Release(r.Context(), chi.URLParam(r, "id"), req.AgentID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tr)
	}
}

// submitRequest is the body of POST /tasks/:id/submit.
type submitRequest struct {
	AgentID string `json:"agent_id"`
	Note    string `json:"note"`
}

func submitTaskHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.TaskService == nil {
			http.Error(w, "task service not wired", http.StatusServiceUnavailable)
			return
		}
		var req submitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		tr, err := deps.TaskService.Submit(r.Context(), chi.URLParam(r, "id"), req.AgentID, req.Note)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tr)
	}
}

// reviewRequest is the body of POST /tasks/:id/review.
type reviewRequest struct {
	Decision string `json:"decision"` // "approve" | "reject"
	Comment  string `json:"comment"`
}

func reviewTaskHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.TaskService == nil {
			http.Error(w, "task service not wired", http.StatusServiceUnavailable)
			return
		}
		var req reviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		userID := ""
		if id, ok := IdentityFrom(r.Context()); ok {
			userID = id.UserID
		}
		tr, err := deps.TaskService.Review(r.Context(), chi.URLParam(r, "id"), userID,
			taskservice.ReviewDecision(req.Decision), req.Comment)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tr)
	}
}

// listTaskCommentsHandler returns comments for a task.
func listTaskCommentsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Comments == nil {
			http.Error(w, "comment service not wired", http.StatusServiceUnavailable)
			return
		}
		got, err := deps.Comments.ListByTarget(r.Context(), comment.TargetTask, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"comments": got})
	}
}

// createTaskCommentHandler adds a comment authored by the session user.
func createTaskCommentHandler(deps Dependencies) http.HandlerFunc {
	type req struct {
		BodyMD string `json:"body_md"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Comments == nil {
			http.Error(w, "comment service not wired", http.StatusServiceUnavailable)
			return
		}
		var in req
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		userID := ""
		if id, ok := IdentityFrom(r.Context()); ok {
			userID = id.UserID
		}
		c := &comment.Comment{
			TargetID:   chi.URLParam(r, "id"),
			AuthorType: comment.AuthorUser,
			AuthorID:   userID,
			BodyMD:     in.BodyMD,
		}
		got, err := deps.Comments.Add(r.Context(), c)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, got)
	}
}

// addTaskAttachmentHandler accepts multipart/form-data with file field.
//
// The handler delegates the file stream + mime + dedup to
// AttachmentService.StoreFromBytes. Size and mime errors are mapped to
// 413 / 415 respectively.
func addTaskAttachmentHandler(deps Dependencies) http.HandlerFunc {
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
			attachment.TargetTask,
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
		writeJSON(w, http.StatusCreated, map[string]any{
			"attachment": res.Attachment,
			"duplicate":  res.Duplicate,
		})
	}
}

// listTaskActivityHandler returns the audit log for a task.
func listTaskActivityHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Activities == nil {
			http.Error(w, "activity service not wired", http.StatusServiceUnavailable)
			return
		}
		got, err := deps.Activities.ListByTask(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"activity": got})
	}
}

// getTaskContextHandler returns a snapshot suitable for an agent
// resuming work: the task, its comments, activity, and subtasks.
func getTaskContextHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		taskID := chi.URLParam(r, "id")
		tr, err := deps.Tasks.GetByID(ctx, taskID)
		if err != nil {
			writeError(w, err)
			return
		}
		out := &TaskContext{Task: tr}
		if deps.Comments != nil {
			out.Comments, _ = deps.Comments.ListByTarget(ctx, comment.TargetTask, taskID)
		}
		if deps.Activities != nil {
			out.Activity, _ = deps.Activities.ListByTask(ctx, taskID)
		}
		if subs, err := deps.Tasks.ListSubtasks(ctx, taskID); err == nil {
			for _, s := range subs {
				out.Subtasks = append(out.Subtasks, *s)
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// keep activity import live even when only some handlers use it.
var _ = activity.ActorUser

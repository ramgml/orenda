// Package api — Phase 3 handlers: task action endpoints + comments,
// attachments, activity, and the agent-facing /context snapshot.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/attachment"
	"github.com/ramgml/orenda/internal/domain/comment"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
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
		// Phase 3 follow-up: tell the project owner an agent picked it up.
		notifyTaskAssignee(r.Context(), deps, "task.assigned_to_me",
			"task.assigned_to_me:"+tr.ID, tr, req.AgentID)
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
		// Notify owner when an agent releases a task it was working on.
		notifyTaskAssignee(r.Context(), deps, "task.released",
			"task.released:"+tr.ID, tr, req.AgentID)
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
		// Phase 6.4: notify the project owner that a task is awaiting review.
		if p, perr := deps.Projects.GetProject(r.Context(), tr.ProjectID); perr == nil && p != nil {
			notifyEvent(r.Context(), deps, notifierservice.Event{
				Type:       "task.review_needed",
				UserID:     p.OwnerID,
				TargetType: "task",
				TargetID:   tr.ID,
				Title:      "Task ready for review",
				Body:       tr.Title,
				Link:       "/tasks/" + tr.ID,
				DedupKey:   "task.review_needed:" + tr.ID,
			})
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
		taskID := chi.URLParam(r, "id")
		c := &comment.Comment{
			TargetID:   taskID,
			AuthorType: comment.AuthorUser,
			AuthorID:   userID,
			BodyMD:     in.BodyMD,
		}
		got, err := deps.Comments.Add(r.Context(), c)
		if err != nil {
			writeError(w, err)
			return
		}
		// Phase 6.4: notify mentioned users.
		if deps.Notifier != nil {
			if mentions, merr := deps.Comments.MentionsForComment(r.Context(), got.ID); merr == nil {
				for _, m := range mentions {
					if string(m.TargetType) != "user" {
						continue
					}
					if m.TargetID == userID {
						continue // don't notify the author
					}
					notifyEvent(r.Context(), deps, notifierservice.Event{
						Type:       "mention.created",
						UserID:     m.TargetID,
						TargetType: "task",
						TargetID:   taskID,
						Title:      "You were mentioned",
						Body:       in.BodyMD,
						Link:       "/tasks/" + taskID,
						DedupKey:   "mention.created:" + got.ID + ":" + m.TargetID,
					})
				}
			}
		}
		writeJSON(w, http.StatusCreated, got)
	}
}

// listTaskAttachmentsHandler returns every attachment uploaded against
// a task. Used by the task detail page to render the file list.
//
// Regression history: this endpoint was accidentally omitted from the
// router — only POST /attachments and GET /attachments/{attId}/download
// were wired. The frontend's `api.listTaskAttachments(taskId)` had been
// calling a non-existent route and chi replied with 405 Method Not
// Allowed. Now both routes live in the same block so a future split
// will surface as a build-time error.
func listTaskAttachmentsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Attachments == nil {
			http.Error(w, "attachment service not wired", http.StatusServiceUnavailable)
			return
		}
		got, err := deps.Attachments.ListByTarget(
			r.Context(), attachment.TargetTask, chi.URLParam(r, "id"),
		)
		if err != nil {
			writeError(w, err)
			return
		}
		if got == nil {
			got = []*attachment.Attachment{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"attachments": got})
	}
}

// downloadAttachmentHandler streams the underlying file to the
// client. The Content-Type is the mime stored at upload time; the
// filename is preserved in Content-Disposition so browsers save
// with the right name.
func downloadAttachmentHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Attachments == nil {
			http.Error(w, "attachment service not wired", http.StatusServiceUnavailable)
			return
		}
		a, f, err := deps.Attachments.Open(r.Context(), chi.URLParam(r, "attId"))
		if err != nil {
			writeError(w, err)
			return
		}
		defer f.Close()

		w.Header().Set("Content-Type", a.Mime)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", a.Size))
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeFilename(a.Filename)))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, f)
	}
}

// sanitizeFilename strips a few control characters and quotes from a
// filename so it can safely live inside an HTTP header value.
func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == 0 || r == 0x7f || r == '"' || r == '\\' || r == '\n' || r == '\r' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
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
		if res.Duplicate {
			w.Header().Set("X-Attachment-Duplicate", "true")
		}
		writeJSON(w, http.StatusCreated, res.Attachment)
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

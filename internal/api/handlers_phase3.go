// Package api — Phase 3 handlers: task action endpoints + comments,
// attachments, activity, and the agent-facing /context snapshot.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/attachment"
	"github.com/ramgml/orenda/internal/domain/comment"
	"github.com/ramgml/orenda/internal/domain/task"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
	taskservice "github.com/ramgml/orenda/internal/service/task"
)

// claimRequest is the body of POST /tasks/:id/claim.
type claimRequest struct {
	AgentID string `json:"agent_id"`
}

func claimTaskHandler(deps *Dependencies) http.HandlerFunc {
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
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		tr, err := deps.TaskService.Claim(r.Context(), taskID, req.AgentID)
		if err != nil {
			if errors.Is(err, taskservice.ErrLockTaken) {
				writeJSON(w, http.StatusConflict, lockTakenResponse(deps, r.Context(), taskID))
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

// lockTakenResponse builds the JSON body for a 409 response when a
// Claim collides with an existing task_locks row.
//
// Phase 15: the old response was just {"error":"lock_taken"} with
// no holder info — the agent had no way to know who to ask. Now
// we look up the holder (if the TaskLockHolder seam is wired) and
// include agent_id / agent_name / acquired_at. The lookup is
// best-effort: if the holder row was deleted between the failed
// Claim and our response, or if the repo isn't wired, we fall
// back to the bare error payload — backwards-compatible with
// anything that was matching on the {error: "lock_taken"} shape.
func lockTakenResponse(deps *Dependencies, ctx context.Context, taskID string) map[string]any {
	out := map[string]any{"error": "lock_taken"}
	if deps.TaskLockHolder == nil {
		return out
	}
	holderID, acquiredAt, err := deps.TaskLockHolder.Holder(ctx, taskID)
	if err != nil || holderID == "" {
		return out
	}
	out["holder_agent_id"] = holderID
	out["claimed_at"] = acquiredAt
	if deps.Agents != nil {
		if a, err := deps.Agents.GetByID(ctx, holderID); err == nil && a != nil {
			out["holder_agent_name"] = a.Name
		}
	}
	return out
}

// populateContextBlockers fills TaskContext.BlockedBy with the
// list of blocker ids whose status is NOT 'done'. The repo returns
// both open and satisfied blockers; we filter to open ones so an
// agent reading the context sees only what's still standing in
// the way.
//
// Phase 15: an agent resuming work needs to know "why can't I
// move this forward?" — the list of unfinished blockers answers
// that directly. Without this, the agent would either claim the
// task and hit a 422 task_blocked later, or have to fetch each
// dependency id separately to figure that out.
func populateContextBlockers(deps *Dependencies, ctx context.Context, taskID string, out *TaskContext) {
	if deps.Tasks == nil {
		return
	}
	rows, err := deps.Tasks.Blockers(ctx, taskID)
	if err != nil {
		return
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		if !r.Done {
			ids = append(ids, r.BlockerID)
		}
	}
	if len(ids) == 0 {
		// Leave nil so the JSON encoder omits the key entirely —
		// the wire shape is then "no blocked_by" rather than
		// "blocked_by: []" which is the same shape as before Phase 15.
		return
	}
	out.BlockedBy = ids
}

// populateContextLockHolder fills TaskContext.LockHolder with the
// current task_locks holder, if any. Phase 15: previously the
// context snapshot had no way to tell the agent "someone else is
// currently working on this" — agents would discover that
// only by failing a claim and reading a 409. Now it's right
// there in the snapshot.
func populateContextLockHolder(deps *Dependencies, ctx context.Context, taskID string, out *TaskContext) {
	if deps.TaskLockHolder == nil {
		return
	}
	holderID, acquiredAt, err := deps.TaskLockHolder.Holder(ctx, taskID)
	if err != nil || holderID == "" {
		return
	}
	out.LockHolder = &LockHolder{
		AgentID:    holderID,
		AcquiredAt: acquiredAt,
	}
	if deps.Agents != nil {
		if a, err := deps.Agents.GetByID(ctx, holderID); err == nil && a != nil {
			out.LockHolder.AgentName = a.Name
		}
	}
}

func releaseTaskHandler(deps *Dependencies) http.HandlerFunc {
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
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		tr, err := deps.TaskService.Release(r.Context(), taskID, req.AgentID)
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

func submitTaskHandler(deps *Dependencies) http.HandlerFunc {
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
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		tr, err := deps.TaskService.Submit(r.Context(), taskID, req.AgentID, req.Note)
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

func reviewTaskHandler(deps *Dependencies) http.HandlerFunc {
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
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		tr, err := deps.TaskService.Review(r.Context(), taskID, userID,
			taskservice.ReviewDecision(req.Decision), req.Comment)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tr)
	}
}

// listTaskCommentsHandler returns comments for a task.
func listTaskCommentsHandler(deps *Dependencies) http.HandlerFunc {
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
func createTaskCommentHandler(deps *Dependencies) http.HandlerFunc {
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
		// Phase 28.5: emit task.commented. The action constant has
		// existed in `internal/domain/activity` since Phase 6, but
		// nothing ever wrote the row — the comment handler was the
		// only mutation that didn't go through taskSvc and therefore
		// didn't get the standard side-effect. We log on failure
		// and keep the 201 going: the comment landed; an audit gap
		// is recoverable, a failed user-visible request isn't.
		if deps.ActivityRecorder != nil {
			payload, _ := json.Marshal(map[string]any{
				"comment_id": got.ID,
				"length":     len(in.BodyMD),
			})
			if rerr := deps.ActivityRecorder.RecordTask(
				r.Context(), taskID,
				activity.ActorUser, userID,
				activity.ActionCommented, string(payload),
			); rerr != nil && deps.Logger != nil {
				deps.Logger.Warn("activity record failed",
					zap.String("action", string(activity.ActionCommented)),
					zap.String("task_id", taskID),
					zap.Error(rerr),
				)
			}
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
func listTaskAttachmentsHandler(deps *Dependencies) http.HandlerFunc {
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
func downloadAttachmentHandler(deps *Dependencies) http.HandlerFunc {
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
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, sanitizeFilename(a.Filename)))
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
func addTaskAttachmentHandler(deps *Dependencies) http.HandlerFunc {
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
		// Phase 28.5: emit task.attachment_added. Same nil-safe +
		// log-on-error pattern as createTaskCommentHandler above.
		// Skip the duplicate-deduplication case: the audit row
		// would point at the *original* attachment (res.Attachment
		// is the existing row), and a second "added" event for the
		// same row would mislead the timeline.
		if !res.Duplicate && deps.ActivityRecorder != nil {
			taskID := chi.URLParam(r, "id")
			payload, _ := json.Marshal(map[string]any{
				"attachment_id": res.Attachment.ID,
				"filename":      filename,
				"mime":          mimeType,
				"size":          res.Attachment.Size,
			})
			if rerr := deps.ActivityRecorder.RecordTask(
				r.Context(), taskID,
				activity.ActorUser, uploaderID,
				activity.ActionAttachmentAdd, string(payload),
			); rerr != nil && deps.Logger != nil {
				deps.Logger.Warn("activity record failed",
					zap.String("action", string(activity.ActionAttachmentAdd)),
					zap.String("task_id", taskID),
					zap.Error(rerr),
				)
			}
		}
		writeJSON(w, http.StatusCreated, res.Attachment)
	}
}

// listTaskActivityHandler returns the audit log for a task.
func listTaskActivityHandler(deps *Dependencies) http.HandlerFunc {
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
// resuming work: the task, its comments, activity, child tasks and
// checklists.
func getTaskContextHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		// Accepts the UUID or the human reference ("#42"/"42"); the
		// snapshot below keys comments/activity/children off the UUID.
		taskID, rerr := resolveTaskRef(ctx, deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
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
		// Children (Phase 14: subtasks are now first-class tasks via
		// parent_task_id) and checklists so the agent sees the same
		// structure the human does.
		if children, err := deps.Tasks.ListChildren(ctx, taskID); err == nil {
			out.Children = children
		}
		if cls, err := deps.Tasks.ListChecklists(ctx, taskID); err == nil {
			out.Checklists = cls
			out.ChecklistItems = map[string][]task.ChecklistItemRow{}
			for _, cl := range cls {
				if its, err := deps.Tasks.ListChecklistItems(ctx, cl.ID); err == nil {
					out.ChecklistItems[cl.ID] = its
				}
			}
		}
		// Phase 15: open blockers and current lock holder. Both are
		// best-effort lookups — empty/nil leaves the field off the
		// wire (the agent reading the context can tell "no blockers"
		// from "this is a free task" without inspecting a sentinel).
		populateContextBlockers(deps, ctx, taskID, out)
		populateContextLockHolder(deps, ctx, taskID, out)
		writeJSON(w, http.StatusOK, out)
	}
}

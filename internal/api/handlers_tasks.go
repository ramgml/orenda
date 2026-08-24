// Package api — task CRUD handlers.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/task"
)

// taskInput is the JSON body for create/patch operations. Pointer fields
// distinguish "absent" from "explicitly empty".
//
// Phase 16 added ProjectID as *string so a PATCH can move a task
// between Inbox (empty) and a real project in one round-trip. The
// create endpoint also accepts it (the inbox handler uses the same
// struct), letting clients file a freshly captured idea under a
// project without a second request.
//
// Phase 13 additions:
//   - Color is *string so a client can send "" (clear the colour label)
//     or omit the field (leave unchanged). The non-pointer form would
//     have no way to express "clear".
//   - Tags is a *[]string pointer too. nil = leave the tag set alone;
//     &[] (empty slice) = clear all tags; non-empty = replace with
//     this exact set. The handler then reconciles with the current
//     set and only fires SetTaskTags + the activity row when the
//     observable state actually changes.
type taskInput struct {
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Status        task.Status       `json:"status"`
	Priority      task.Priority     `json:"priority"`
	AssigneeType  task.AssigneeType `json:"assignee_type"`
	AssigneeID    string            `json:"assignee_id"`
	ProjectID     *string           `json:"project_id"` // Phase 16: nullable
	ColumnID      string            `json:"column_id"`
	ParentTaskID  string            `json:"parent_task_id"`
	ContextMD     string            `json:"context_md"`
	AgentNotes    string            `json:"agent_notes"`
	Color         *string           `json:"color"`
	Tags          *[]string         `json:"tags"`
	DueAt         *string           `json:"due_at"` // RFC3339 or null
	StartedAt     *string           `json:"started_at"`
	ClaimedAt     *string           `json:"claimed_at"`
	CompletedAt   *string           `json:"completed_at"`
	TimeEstimateS *int              `json:"time_estimate_s"`
	TimeSpentS    *int              `json:"time_spent_s"`
	Position      *float64          `json:"position"`
}

// listProjectTasksHandler returns tasks for a project, optionally filtered by
// status or column via query params.
//
// Phase 17: the endpoint hydrates per-task Counters (comments /
// attachments / children / checklist_items) and BlockedByCount so the
// kanban card renders without per-card fetches. Used by the
// kanban board and the inbox list.
func listProjectTasksHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := resolveProjectRef(r.Context(), deps, chi.URLParam(r, "id"))
		if err != nil {
			writeProjectResolveError(w, err)
			return
		}
		f := task.Filter{
			ProjectID: p.ID,
		}
		if s := r.URL.Query().Get("status"); s != "" {
			f.Status = task.Status(s)
		}
		if c := r.URL.Query().Get("column_id"); c != "" {
			f.ColumnID = c
		}
		tasks, err := deps.Tasks.ListByProjectWithStats(r.Context(), f)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
	}
}

// createTaskHandler creates a task in a project.
//
// Phase 16: the body may carry `project_id` to override the URL param.
// That's how the inbox endpoint (POST /api/v1/inbox/tasks) calls into
// the same struct without a separate code path. We still take the
// project from the URL by default — most clients don't send it.
func createTaskHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in taskInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		projectID := chi.URLParam(r, "id")
		if in.ProjectID != nil {
			projectID = *in.ProjectID
		}
		// Resolve project ref (P<N> or UUID) to UUID.
		resolved, err := resolveProjectRef(r.Context(), deps, projectID)
		if err != nil {
			writeProjectResolveError(w, err)
			return
		}
		projectID = resolved.ID
		tr := &task.Task{
			ProjectID:     projectID,
			ColumnID:      in.ColumnID,
			ParentTaskID:  in.ParentTaskID,
			Title:         in.Title,
			Description:   in.Description,
			Status:        in.Status,
			Priority:      in.Priority,
			AssigneeType:  in.AssigneeType,
			AssigneeID:    in.AssigneeID,
			ContextMD:     in.ContextMD,
			AgentNotes:    in.AgentNotes,
			CreatedByType: task.CreatorUser,
		}
		if in.DueAt != nil {
			tr.DueAt = parseOptionalTime(*in.DueAt)
		}
		if in.StartedAt != nil {
			tr.StartedAt = parseOptionalTime(*in.StartedAt)
		}
		if in.ClaimedAt != nil {
			tr.ClaimedAt = parseOptionalTime(*in.ClaimedAt)
		}
		if in.CompletedAt != nil {
			tr.CompletedAt = parseOptionalTime(*in.CompletedAt)
		}
		if in.TimeEstimateS != nil {
			v := *in.TimeEstimateS
			tr.TimeEstimateS = &v
		}
		if in.Position != nil {
			tr.Position = *in.Position
		}

		// Phase 14 board UX: child tasks inherit their parent's column
		// (or fall back to the first column of the project) so they
		// appear on the kanban immediately. Without this, children
		// would be created with column_id=NULL and float off-board
		// until somebody dragged them manually. The frontend already
		// knows the parent's column and passes it through, so this is
		// just a safety net for API users who don't.
		//
		// Phase 16: when the parent itself is an Inbox task (no
		// column), we don't fall back to the project's first column —
		// there's no project, so FirstColumnID returns "" and the
		// child stays off-board by design (the parent is a flat list,
		// not a kanban).
		if tr.ParentTaskID != "" && tr.ColumnID == "" {
			if parent, err := deps.Tasks.GetByID(r.Context(), tr.ParentTaskID); err == nil && parent.ColumnID != "" {
				tr.ColumnID = parent.ColumnID
			} else if tr.ProjectID != "" {
				if colID, err := deps.Tasks.FirstColumnID(r.Context(), tr.ProjectID); err == nil {
					tr.ColumnID = colID
				}
			}
		}

		if err := deps.Tasks.Create(r.Context(), tr); err != nil {
			writeError(w, err)
			return
		}
		if deps.TaskService != nil {
			deps.TaskService.MirrorSave(r.Context(), tr)
		}
		// Phase 14: when the new task is a child of an existing parent,
		// record the creation against the parent's activity log so the
		// parent task's timeline shows the child being added.
		if tr.ParentTaskID != "" && deps.TaskService != nil {
			actorID := ""
			if id, ok := IdentityFrom(r.Context()); ok && id != nil {
				actorID = id.UserID
			}
			deps.TaskService.RecordActivity(
				r.Context(), tr.ParentTaskID, actorID,
				activity.ActionChildAdded,
				fmt.Sprintf(`{"child_id":%q,"title":%q}`, tr.ID, tr.Title),
			)
		}
		writeJSON(w, http.StatusCreated, tr)
	}
}

// getTaskHandler returns one task.
//
// Phase 27.3: also populates the Tags slice so the task view and the
// inbox/sidebar reader see the same tag set the kanban card renders
// (chips on the side panel stay consistent with chips on the card).
func getTaskHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Accepts the UUID or the T-prefixed ref ("T42").
		tr, err := task.ResolveRef(r.Context(), deps.Tasks, chi.URLParam(r, "id"))
		if err != nil {
			writeResolveError(w, err)
			return
		}
		// Tags don't live on the task row; we'd need a separate read.
		// Phase 27.3 pins this: a single-task GET returns the tags
		// the user sees in the card. Nil-check guarded — empty tag
		// sets render as `tags: []`, the absence we promise via
		// `omitempty` only kicks in for truly unmarshalled fields.
		if tags, terr := deps.Tasks.ListTagsForTask(r.Context(), tr.ID); terr == nil {
			tr.Tags = tags
		}
		writeJSON(w, http.StatusOK, tr)
	}
}

// patchTaskHandler updates mutable task fields (PATCH and PUT both work).
func patchTaskHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Accepts the UUID or the T-prefixed ref ("T42").
		tr, err := task.ResolveRef(r.Context(), deps.Tasks, chi.URLParam(r, "id"))
		if err != nil {
			writeResolveError(w, err)
			return
		}
		var in taskInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}

		actorID := ""
		if id, ok := IdentityFrom(r.Context()); ok && id != nil {
			actorID = id.UserID
		}
		if err := applyTaskPatchAndEffects(r.Context(), deps, tr, in, actorID); err != nil {
			writeError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, tr)

		// Phase 30.14: publish task.updated on the `tasks` topic so a
		// second tab (or a user who navigates away and back) sees the
		// changes a sibling tab made through PATCH. The agent-flow
		// events already publish (claim/submit/review/deps_changed);
		// PATCH had no WS broadcast, which is the long-standing
		// cross-tab visibility gap. Bulk-edit shares the same emit so
		// the kanban bulk-action bar doesn't need a separate path.
		if deps.WSHub != nil {
			deps.WSHub.Publish(r.Context(), ws.Event{
				Topic: "tasks",
				Body: map[string]any{
					"type": "task.updated",
					"task": tr,
				},
			})
		}
	}
}

// applyTaskPatchAndEffects is the shared mutation path for single and bulk
// task edits. Keeping side effects here prevents bulk updates from silently
// bypassing completion timestamps, awaiting normalization, mirrors, or audit
// rows.
func applyTaskPatchAndEffects(ctx context.Context, deps *Dependencies, tr *task.Task, in taskInput, actorID string) error {
	prevColor := tr.Color
	prevStatus := tr.Status
	prevPriority := tr.Priority
	prevAssigneeType := tr.AssigneeType
	prevAssigneeID := tr.AssigneeID

	if err := applyTaskPatch(ctx, deps, tr, in); err != nil {
		return err
	}
	statusChanged := in.Status != "" && tr.Status != prevStatus
	if statusChanged && tr.Status == task.StatusDone && in.CompletedAt == nil {
		now := time.Now().UTC()
		tr.CompletedAt = &now
	}
	if statusChanged {
		switch tr.Status {
		case task.StatusDone:
			tr.Awaiting = task.AwaitingNone
		case task.StatusReview:
			tr.Awaiting = task.AwaitingHuman
		default:
			tr.Awaiting = task.AwaitingNone
		}
	}
	// T46: centralize status↔column sync + persist + mirror + activity
	// in SyncAndSave instead of direct Tasks.Update.
	if deps.TaskService != nil {
		if err := deps.TaskService.SyncAndSave(ctx, tr, actorID, activity.ActorUser, prevStatus); err != nil {
			return err
		}
	} else {
		// Fallback when TaskService is not wired (partial test fixtures).
		if err := deps.Tasks.Update(ctx, tr); err != nil {
			return err
		}
	}
	if in.Color != nil && prevColor != tr.Color && deps.TaskService != nil {
		deps.TaskService.RecordActivity(ctx, tr.ID, actorID, activity.ActionColorChanged,
			fmt.Sprintf(`{"from":%q,"to":%q}`, prevColor, tr.Color))
	}
	if in.Tags != nil {
		applyTaskTagsChange(ctx, deps, tr.ID, *in.Tags)
	}
	if deps.TaskService != nil {
		// Status change activity is now recorded by SyncAndSave.
		if in.Priority != "" && tr.Priority != prevPriority {
			deps.TaskService.RecordActivity(ctx, tr.ID, actorID, activity.ActionPriorityChanged,
				fmt.Sprintf(`{"from":%q,"to":%q}`, prevPriority, tr.Priority))
		}
		if (in.AssigneeType != "" || in.AssigneeID != "") &&
			(tr.AssigneeType != prevAssigneeType || tr.AssigneeID != prevAssigneeID) {
			deps.TaskService.RecordActivity(ctx, tr.ID, actorID, activity.ActionAssigned,
				fmt.Sprintf(`{"from":{"type":%q,"id":%q},"to":{"type":%q,"id":%q}}`,
					prevAssigneeType, prevAssigneeID, tr.AssigneeType, tr.AssigneeID))
		}
	}
	return nil
}

type bulkTaskPatchInput struct {
	TaskIDs []string  `json:"task_ids"`
	Patch   taskInput `json:"patch"`
}

type bulkTaskPatchResponse struct {
	Tasks  []*task.Task      `json:"tasks"`
	Errors map[string]string `json:"errors,omitempty"`
}

// bulkPatchTasksHandler applies the same PATCH semantics to several tasks.
// The operation is best-effort: one malformed or missing task does not hide
// successful updates to the other selected tasks; the response identifies
// failures by task id. Every successful row emits task.updated so open boards
// refetch through their existing WebSocket subscription.
func bulkPatchTasksHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in bulkTaskPatchInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if len(in.TaskIDs) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_ids_required"})
			return
		}
		seen := make(map[string]struct{}, len(in.TaskIDs))
		for _, id := range in.TaskIDs {
			if id == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_id_required"})
				return
			}
			if _, ok := seen[id]; ok {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "duplicate_task_id"})
				return
			}
			seen[id] = struct{}{}
		}

		actorID := ""
		if id, ok := IdentityFrom(r.Context()); ok && id != nil {
			actorID = id.UserID
		}
		out := bulkTaskPatchResponse{Tasks: make([]*task.Task, 0, len(in.TaskIDs)), Errors: make(map[string]string)}
		for _, id := range in.TaskIDs {
			tr, err := deps.Tasks.GetByID(r.Context(), id)
			if err != nil {
				out.Errors[id] = err.Error()
				continue
			}
			if err := applyTaskPatchAndEffects(r.Context(), deps, tr, in.Patch, actorID); err != nil {
				out.Errors[id] = err.Error()
				continue
			}
			out.Tasks = append(out.Tasks, tr)
			if deps.WSHub != nil {
				deps.WSHub.Publish(r.Context(), ws.Event{
					Topic: "tasks",
					Body:  map[string]any{"type": "task.updated", "task": tr},
				})
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// applyTaskPatch mutates tr based on in.
//
// Non-pointer string fields are only updated when non-empty so the caller
// can send a partial document (PATCH semantics). Pointer fields carry
// explicit null intent.
//
// Phase 16 — project transitions:
//
//   - in.ProjectID == nil                → leave project_id alone.
//   - *in.ProjectID == "" (explicit)     → file task under Inbox; clear
//     column_id (inbox cards have no board).
//   - *in.ProjectID != "" && unchanged   → no column re-resolution
//     (caller may have moved columns within the same project).
//   - *in.ProjectID != "" && changed     → assign column_id = first
//     column of the new project unless the caller also set
//     column_id explicitly in the same PATCH.
//
// The column-resolution policy mirrors the create handler so the
// UX is consistent: dropping an inbox card onto a board always
// lands it in the first column.
func applyTaskPatch(ctx context.Context, deps *Dependencies, tr *task.Task, in taskInput) error {
	if in.Title != "" {
		tr.Title = in.Title
	}
	if in.Description != "" {
		tr.Description = in.Description
	}
	if in.Status != "" {
		tr.Status = in.Status
	}
	if in.Priority != "" {
		tr.Priority = in.Priority
	}
	if in.ParentTaskID != "" {
		tr.ParentTaskID = in.ParentTaskID
	}
	if in.ContextMD != "" {
		tr.ContextMD = in.ContextMD
	}
	if in.AgentNotes != "" {
		tr.AgentNotes = in.AgentNotes
	}
	if in.AssigneeType == "unassigned" {
		tr.AssigneeType = ""
		tr.AssigneeID = ""
	} else if in.AssigneeType != "" {
		tr.AssigneeType = in.AssigneeType
	}
	if in.AssigneeID != "" {
		tr.AssigneeID = in.AssigneeID
	}
	// Project transition (Phase 16). Decides whether to also touch
	// column_id so the task lands on a real column instead of dangling.
	if in.ProjectID != nil && *in.ProjectID != tr.ProjectID {
		newProject := *in.ProjectID
		// Resolve project ref (P<N> or UUID) to UUID when non-empty.
		if newProject != "" {
			resolved, err := resolveProjectRef(ctx, deps, newProject)
			if err != nil {
				return err
			}
			newProject = resolved.ID
		}
		tr.ProjectID = newProject
		if in.ColumnID == "" {
			// No explicit column in this PATCH — derive from the
			// new project. Empty (inbox) clears column_id.
			if newProject == "" {
				tr.ColumnID = ""
			} else if colID, err := deps.Tasks.FirstColumnID(ctx, newProject); err == nil {
				tr.ColumnID = colID
			}
		}
	}
	if in.ColumnID != "" {
		tr.ColumnID = in.ColumnID
	}

	// Phase 27.8 / T46: column→status sync when the user explicitly
	// changed column_id (e.g. DnD). Status→column is handled by
	// Service.SyncAndSave (called by applyTaskPatchAndEffects).
	if in.ColumnID != "" && in.Status == "" && deps.Projects != nil {
		if dest, err := deps.Projects.GetColumn(ctx, in.ColumnID); err == nil && dest != nil && dest.Status != "" {
			tr.Status = task.Status(dest.Status)
		}
	}
	if in.Color != nil {
		// PATCH: *string so an empty value explicitly clears the
		// colour label. We don't validate the format here; the
		// <input type="color"> element on the frontend enforces it
		// and a bogus value just renders as black in CSS.
		tr.Color = *in.Color
	}
	if in.DueAt != nil {
		tr.DueAt = parseOptionalTime(*in.DueAt)
	}
	if in.StartedAt != nil {
		tr.StartedAt = parseOptionalTime(*in.StartedAt)
	}
	if in.ClaimedAt != nil {
		tr.ClaimedAt = parseOptionalTime(*in.ClaimedAt)
	}
	if in.CompletedAt != nil {
		tr.CompletedAt = parseOptionalTime(*in.CompletedAt)
	}
	if in.TimeEstimateS != nil {
		v := *in.TimeEstimateS
		tr.TimeEstimateS = &v
	}
	if in.TimeSpentS != nil {
		tr.TimeSpentS = *in.TimeSpentS
	}
	if in.Position != nil {
		tr.Position = *in.Position
	}
	return nil
}
func deleteTaskHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := deps.Tasks.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeError(w, err)
			return
		}
		if deps.TaskService != nil {
			deps.TaskService.MirrorDelete(chi.URLParam(r, "id"))
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// listChildTasksHandler returns the direct children of a parent task
// together with a {total, done} progress snapshot used by the
// ChildTasksList UI to draw the progress bar.
//
// Phase 14 replacement for listSubtasksHandler: subtasks are now
// first-class tasks via parent_task_id, so the UI shows them as
// cards (with status / assignee / click-to-open) rather than flat
// checkboxes.
func listChildTasksHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		parentID := chi.URLParam(r, "id")
		children, err := deps.Tasks.ListChildren(ctx, parentID)
		if err != nil {
			writeError(w, err)
			return
		}
		total, done, err := deps.Tasks.ChildProgress(ctx, parentID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tasks": children,
			"progress": map[string]int{
				"total": total,
				"done":  done,
			},
		})
	}
}

// ---------------------------------------------------------------------------
// Checklists
// ---------------------------------------------------------------------------

func listChecklistsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := deps.Tasks.ListChecklists(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"checklists": rows})
	}
}

func addChecklistHandler(deps *Dependencies) http.HandlerFunc {
	type in struct {
		Title    string `json:"title"`
		Position int    `json:"position"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var body in
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if body.Title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_title"})
			return
		}
		taskID := chi.URLParam(r, "id")
		row, err := deps.Tasks.AddChecklist(r.Context(), taskID, body.Title)
		if err != nil {
			writeError(w, err)
			return
		}
		// Phase 14: log checklist creation on the parent task's activity
		// stream so the timeline is informative without polling.
		if deps.TaskService != nil {
			actorID := ""
			if id, ok := IdentityFrom(r.Context()); ok && id != nil {
				actorID = id.UserID
			}
			deps.TaskService.RecordActivity(
				r.Context(), taskID, actorID,
				activity.ActionChecklistAdded,
				fmt.Sprintf(`{"checklist_id":%q,"title":%q}`, row.ID, row.Title),
			)
		}
		writeJSON(w, http.StatusCreated, row)
	}
}

func deleteChecklistHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := deps.Tasks.DeleteChecklist(r.Context(), chi.URLParam(r, "clId")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listChecklistItemsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := deps.Tasks.ListChecklistItems(r.Context(), chi.URLParam(r, "clId"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": rows})
	}
}

func addChecklistItemHandler(deps *Dependencies) http.HandlerFunc {
	type in struct {
		Title    string `json:"title"`
		Position int    `json:"position"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var body in
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if body.Title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_title"})
			return
		}
		taskID := chi.URLParam(r, "id")
		listID := chi.URLParam(r, "clId")
		row, err := deps.Tasks.AddChecklistItem(r.Context(), listID, body.Title)
		if err != nil {
			writeError(w, err)
			return
		}
		// Phase 14: emit item_added against the parent task so the
		// activity log shows checklist work without per-row polling.
		if deps.TaskService != nil {
			actorID := ""
			if id, ok := IdentityFrom(r.Context()); ok && id != nil {
				actorID = id.UserID
			}
			deps.TaskService.RecordActivity(
				r.Context(), taskID, actorID,
				activity.ActionChecklistItemAdded,
				fmt.Sprintf(`{"checklist_id":%q,"item_id":%q,"title":%q}`, listID, row.ID, row.Title),
			)
		}
		writeJSON(w, http.StatusCreated, row)
	}
}

type updateChecklistItemInput struct {
	Title *string `json:"title"`
	Done  *bool   `json:"done"`
}

func updateChecklistItemHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body updateChecklistItemInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		itemID := chi.URLParam(r, "itemId")
		taskID := chi.URLParam(r, "id")
		if err := deps.Tasks.UpdateChecklistItem(r.Context(), itemID, body.Done, body.Title); err != nil {
			writeError(w, err)
			return
		}
		// Phase 14: emit checklist_item_done only when toggling done
		// (not on title-only edits). The activity stream then doubles
		// as a lightweight "what got checked off" feed.
		if body.Done != nil && *body.Done && deps.TaskService != nil {
			actorID := ""
			if id, ok := IdentityFrom(r.Context()); ok && id != nil {
				actorID = id.UserID
			}
			deps.TaskService.RecordActivity(
				r.Context(), taskID, actorID,
				activity.ActionChecklistItemDone,
				fmt.Sprintf(`{"item_id":%q,"done":true}`, itemID),
			)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func deleteChecklistItemHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := deps.Tasks.DeleteChecklistItem(r.Context(), chi.URLParam(r, "itemId")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// updateSubtaskHandler removed in Phase 14 — child tasks are full
// tasks; use PATCH /api/v1/tasks/{id} instead.

// deleteSubtaskHandler removed in Phase 14 — child tasks are full
// tasks; use DELETE /api/v1/tasks/{id} instead.

// tasksWithDueHandler lists tasks whose due_at falls within [from, to].
// Phase 30.8: the calendar needs to render tasks alongside timed
// events; the simplest seam is a dedicated endpoint that returns
// just the tasks with a due_at in the requested window. We don't
// filter by `done` status — done tasks appear shaded in the UI as
// "completed on this date" — see the calendar's render layer.
//
// The endpoint is intentionally narrow: a single From/To window
// keyed off the existing tasks repo. We don't paginate because the
// realistic upper bound is a few hundred tasks per month (single
// owner, single instance).
func tasksWithDueHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, err := parseTimeQuery(r, "from")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_from"})
			return
		}
		to, err := parseTimeQuery(r, "to")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_to"})
			return
		}
		tasks, err := deps.Tasks.ListByDueBetween(r.Context(), from, to)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
	}
}

// parseTimeQuery reads an RFC3339 time from r.URL.Query(). Empty
// returns the zero value (caller decides whether that's an error).
func parseTimeQuery(r *http.Request, key string) (time.Time, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return time.Time{}, fmt.Errorf("missing %q", key)
	}
	return time.Parse(time.RFC3339, raw)
}

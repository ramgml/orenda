// Package api — Phase 8 offline sync endpoint.
//
// POST /api/v1/sync accepts a batch of operations queued by the PWA
// outbox. Each operation is applied in order; the response lists the
// per-op outcome keyed by client_id (the PWA's idempotency key).
//
// Conflict resolution (8.6): last-write-wins by `updated_at` is implicit —
// we apply ops in arrival order and the later write wins. Idempotency is
// achieved via a sync_ops table that records applied client_ids so a
// retry doesn't double-create.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ramgml/orenda/internal/domain/comment"
	"github.com/ramgml/orenda/internal/domain/event"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/wiki"
	taskservice "github.com/ramgml/orenda/internal/service/task"
)

// syncRequest is the POST body.
type syncRequest struct {
	Ops []syncOp `json:"ops"`
}

// syncOp is one queued mutation.
type syncOp struct {
	Op        string          `json:"op"`     // create_task | update_task | move_task | create_comment | create_event | create_page
	Target    string          `json:"target"` // project_id or task_id
	Payload   json.RawMessage `json:"payload"`
	ClientID  string          `json:"client_id"`
	CreatedAt time.Time       `json:"created_at"`
}

// syncResponse is the reply.
type syncResponse struct {
	Results []syncResult `json:"results"`
}

// syncResult is the per-op outcome.
type syncResult struct {
	ClientID string `json:"client_id"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	ID       string `json:"id,omitempty"` // server-assigned id for create ops
}

// syncHandler applies a batch of offline operations.
func syncHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req syncRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if len(req.Ops) == 0 {
			writeJSON(w, http.StatusOK, syncResponse{Results: []syncResult{}})
			return
		}
		if len(req.Ops) > 200 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too_many_ops"})
			return
		}

		id, ok := IdentityFrom(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no_identity"})
			return
		}

		resp := syncResponse{Results: make([]syncResult, 0, len(req.Ops))}
		for _, op := range req.Ops {
			resp.Results = append(resp.Results, applySyncOp(r, deps, id, op))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// applySyncOp dispatches a single op and reports its outcome.
//
// Already-applied ops (same client_id) are reported as ok with the
// previously created id — that's the idempotency guarantee the PWA
// relies on.
func applySyncOp(r *http.Request, deps Dependencies, id *Identity, op syncOp) syncResult {
	ctx := r.Context()
	res := syncResult{ClientID: op.ClientID}

	if op.ClientID == "" {
		res.Error = "missing_client_id"
		return res
	}
	if seen, idBefore, err := syncOpsSeen(ctx, deps, op.ClientID); err == nil && seen {
		res.OK = true
		res.ID = idBefore
		return res
	}

	switch op.Op {
	case "create_task":
		var in struct {
			Title       string  `json:"title"`
			ColumnID    string  `json:"column_id"`
			Description string  `json:"description"`
			ProjectID   *string `json:"project_id,omitempty"` // Phase 16: optional
		}
		if err := json.Unmarshal(op.Payload, &in); err != nil || in.Title == "" {
			res.Error = "invalid_payload"
			return res
		}
		// ProjectID resolution order (Phase 16):
		//   1. body.project_id (explicit)        wins
		//   2. op.Target (the request URL slug) — preserves the older
		//      single-target semantics for clients that pin the URL
		//   3. "" (Inbox) — the new default for "I just want to
		//      capture this idea"
		projectID := ""
		if in.ProjectID != nil {
			projectID = *in.ProjectID
		} else if op.Target != "" {
			projectID = op.Target
		}
		tr := &task.Task{
			ProjectID:   projectID,
			ColumnID:    in.ColumnID,
			Title:       in.Title,
			Description: in.Description,
		}
		if err := deps.Tasks.Create(ctx, tr); err != nil {
			res.Error = err.Error()
			return res
		}
		if deps.TaskService != nil {
			deps.TaskService.MirrorSave(ctx, tr)
		}
		_ = syncOpsRecord(ctx, deps, op.ClientID, tr.ID)
		res.OK = true
		res.ID = tr.ID
		return res

	case "update_task":
		tr, err := deps.Tasks.GetByID(ctx, op.Target)
		if err != nil {
			res.Error = "not_found"
			return res
		}
		// Apply payload fields. Last-write-wins per PLAN#8.6 — we don't
		// compare updated_at since ops arrive in chronological order.
		var in struct {
			Title       string `json:"title,omitempty"`
			Description string `json:"description,omitempty"`
			Status      string `json:"status,omitempty"`
			Priority    string `json:"priority,omitempty"`
		}
		if err := json.Unmarshal(op.Payload, &in); err != nil {
			res.Error = "invalid_payload"
			return res
		}
		if in.Title != "" {
			tr.Title = in.Title
		}
		if in.Description != "" {
			tr.Description = in.Description
		}
		if in.Status != "" {
			tr.Status = task.Status(in.Status)
		}
		if in.Priority != "" {
			tr.Priority = task.Priority(in.Priority)
		}
		if err := deps.Tasks.Update(ctx, tr); err != nil {
			res.Error = err.Error()
			return res
		}
		if deps.TaskService != nil {
			deps.TaskService.MirrorSave(ctx, tr)
		}
		_ = syncOpsRecord(ctx, deps, op.ClientID, tr.ID)
		res.OK = true
		res.ID = tr.ID
		return res

	case "move_task":
		var in struct {
			ColumnID string `json:"column_id"`
		}
		if err := json.Unmarshal(op.Payload, &in); err != nil || in.ColumnID == "" {
			res.Error = "invalid_payload"
			return res
		}
		if deps.TaskService == nil {
			res.Error = "service_not_wired"
			return res
		}
		tr, err := deps.TaskService.Move(ctx, op.Target, taskservice.MoveOptions{TargetColumnID: in.ColumnID})
		if err != nil {
			res.Error = err.Error()
			return res
		}
		_ = syncOpsRecord(ctx, deps, op.ClientID, tr.ID)
		res.OK = true
		res.ID = tr.ID
		return res

	case "create_comment":
		var in struct {
			BodyMD string `json:"body_md"`
		}
		if err := json.Unmarshal(op.Payload, &in); err != nil || in.BodyMD == "" {
			res.Error = "invalid_payload"
			return res
		}
		if deps.Comments == nil {
			res.Error = "service_not_wired"
			return res
		}
		c := &comment.Comment{
			TargetID:   op.Target,
			AuthorType: comment.AuthorUser,
			AuthorID:   id.UserID,
			BodyMD:     in.BodyMD,
		}
		got, err := deps.Comments.Add(ctx, c)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		_ = syncOpsRecord(ctx, deps, op.ClientID, got.ID)
		res.OK = true
		res.ID = got.ID
		return res

	case "create_event":
		var in struct {
			Title     string    `json:"title"`
			StartAt   time.Time `json:"start_at"`
			EndAt     time.Time `json:"end_at"`
			AllDay    bool      `json:"all_day"`
			Color     string    `json:"color"`
			ProjectID string    `json:"project_id"`
		}
		if err := json.Unmarshal(op.Payload, &in); err != nil || in.Title == "" {
			res.Error = "invalid_payload"
			return res
		}
		if deps.EventService == nil {
			res.Error = "service_not_wired"
			return res
		}
		ev := &event.Event{
			Title:   in.Title,
			StartAt: in.StartAt,
			EndAt:   in.EndAt,
			AllDay:  in.AllDay,
			Color:   in.Color,
		}
		if in.ProjectID != "" {
			ev.ProjectID = in.ProjectID
		}
		got, err := deps.EventService.Create(ctx, ev)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		_ = syncOpsRecord(ctx, deps, op.ClientID, got.ID)
		res.OK = true
		res.ID = got.ID
		return res

	case "create_page":
		var in struct {
			Slug      string `json:"slug"`
			Title     string `json:"title"`
			ContentMD string `json:"content_md"`
		}
		if err := json.Unmarshal(op.Payload, &in); err != nil || in.Slug == "" {
			res.Error = "invalid_payload"
			return res
		}
		if deps.WikiService == nil {
			res.Error = "service_not_wired"
			return res
		}
		got, err := deps.WikiService.Save(ctx, &wiki.Page{
			Slug:      in.Slug,
			Title:     in.Title,
			ContentMD: in.ContentMD,
		})
		if err != nil {
			res.Error = err.Error()
			return res
		}
		_ = syncOpsRecord(ctx, deps, op.ClientID, got.ID)
		res.OK = true
		res.ID = got.ID
		return res
	}

	res.Error = "unsupported_op"
	return res
}

// ----------------------------------------------------------------------------
// sync_ops persistence — tracks applied client_ids for idempotency.
// ----------------------------------------------------------------------------

// syncOpsSeen reports whether this client_id was already applied; returns
// the server id created by the earlier application.
func syncOpsSeen(ctx context.Context, deps Dependencies, clientID string) (bool, string, error) {
	if deps.SyncOps == nil {
		return false, "", nil
	}
	return deps.SyncOps.Seen(ctx, clientID)
}

// syncOpsRecord records an applied op.
func syncOpsRecord(ctx context.Context, deps Dependencies, clientID, serverID string) error {
	if deps.SyncOps == nil {
		return nil
	}
	return deps.SyncOps.Record(ctx, clientID, serverID)
}

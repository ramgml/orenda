// Package api — agent-namespace aliases for endpoints that already
// exist under /api/v1/* with cookie auth.
//
// Phase 27.11 closes an audit-debt (PLAN §27.11): the agent CLI's
// `comment` and `await` commands posted to user-cookie routes
// (`/api/v1/tasks/{id}/comments` and `/api/v1/events/await`), which
// only accept a JWT session — agent tokens got 401. The fix is to
// mount the same handler logic under the agent namespace, with
// the author / identity derived from `Identity.AgentID` instead
// of `Identity.UserID`.
//
// We don't share the existing user-side handlers by aliasing them:
// the comment handler would need to know which AuthorType to write
// (user vs agent), and the await handler needs to filter events
// by the agent's id, not the user's. Both decisions live in the
// Identity, so the agent version reads `id.AgentID` and writes
// `AuthorAgent` — the routing is the only thing that differs.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/comment"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
)

// agentCreateTaskCommentHandler mirrors createTaskCommentHandler but
// writes comments as the agent's author identity. The handler is
// mounted under RequireAgent, so the bearer token has already been
// resolved to an Identity with `AgentID` set; we use that id as the
// comment author and the OwnerID for any mentions-notification path.
func agentCreateTaskCommentHandler(deps *Dependencies) http.HandlerFunc {
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
		id, ok := IdentityFrom(r.Context())
		if !ok || id == nil || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// "#42"/"42" resolve to the task UUID before the comment is
		// written — same convention as the other agent task routes.
		taskID, rerr := resolveTaskRef(r.Context(), deps, chi.URLParam(r, "id"))
		if rerr != nil {
			writeResolveError(w, rerr)
			return
		}
		c := &comment.Comment{
			TargetID:   taskID,
			AuthorType: comment.AuthorAgent,
			AuthorID:   id.AgentID,
			BodyMD:     in.BodyMD,
		}
		got, err := deps.Comments.Add(r.Context(), c)
		if err != nil {
			writeError(w, err)
			return
		}
		// Phase 28.5: emit task.commented from the agent side too.
		// Same nil-safe + log-on-error pattern as the user-side
		// handler. ActorType is `agent` so the timeline can colour
		// human vs agent comments distinctly.
		if deps.ActivityRecorder != nil {
			payload, _ := json.Marshal(map[string]any{
				"comment_id":  got.ID,
				"author_type": "agent",
				"length":      len(in.BodyMD),
			})
			if rerr := deps.ActivityRecorder.RecordTask(
				r.Context(), taskID,
				activity.ActorAgent, id.AgentID,
				activity.ActionCommented, string(payload),
			); rerr != nil && deps.Logger != nil {
				deps.Logger.Warn("activity record failed",
					zap.String("action", string(activity.ActionCommented)),
					zap.String("task_id", taskID),
					zap.Error(rerr),
				)
			}
		}
		// Phase 6.4: notify mentioned users (owner only — agent → user
		// mentions are the only direction we route today; agent-to-agent
		// isn't a real flow because single-owner deployments only have
		// one user identity).
		if deps.Notifier != nil {
			if mentions, merr := deps.Comments.MentionsForComment(r.Context(), got.ID); merr == nil {
				for _, m := range mentions {
					if string(m.TargetType) != "user" {
						continue
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

// agentAwaitRequest is the JSON body of POST /api/v1/agent/events/await.
//
// Same shape as the user-side awaitRequest so the CLI doesn't need a
// different request struct per namespace — but the UserID field is
// optional; when missing the handler uses the agent's id, which the
// event filter in the hub applies (`user_id == agentID`).
type agentAwaitRequest struct {
	Topic      string `json:"topic"`
	UserID     string `json:"user_id,omitempty"`
	TimeoutSec int    `json:"timeout_s"`
}

// agentAwaitHandler mirrors awaitHandler but subscribes under the
// agent's id. Pre-27.11, posting to /events/await with an agent
// bearer token got 401 (RequireUser only accepts cookie/JWT). The
// agent namespace provides the same one-shot long-poll shape and
// reuses the WS hub; events are filtered by user_id, so the agent
// only sees events whose payload references its own id (matches the
// publishing side: claim / submit / review all emit user_id=agentID).
func agentAwaitHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id == nil || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req agentAwaitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		timeout := req.TimeoutSec
		if timeout <= 0 {
			timeout = 30
		}
		if timeout > 60 {
			timeout = 60
		}
		if deps.WSHub == nil {
			http.Error(w, "ws hub not configured", http.StatusServiceUnavailable)
			return
		}

		// Scope to the agent's id unless the caller overrides. The hub's
		// Subscribe uses user_id as the filter key; agents receive only
		// events whose payload's user_id matches the agent id (the
		// publishing side fills user_id with the agent's id on
		// claim/submit/review).
		userID := req.UserID
		if userID == "" {
			userID = id.AgentID
		}

		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
		defer cancel()

		events, unsub := deps.WSHub.Subscribe(userID, req.Topic)
		defer unsub()

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Error(w, ctx.Err().Error(), http.StatusInternalServerError)
			return
		case ev, ok := <-events:
			if !ok {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeJSON(w, http.StatusOK, awaitResponse{Topic: ev.Topic, Body: ev.Body})
		}
	}
}

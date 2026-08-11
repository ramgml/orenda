// Package api — notification endpoints + helpers.
package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/ramgml/orenda/internal/domain/task"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
)

// notifyEvent is a small helper that calls Notifier.Notify with a typed
// event. Errors are swallowed — notifications are best-effort in Phase 6.
func notifyEvent(ctx context.Context, deps Dependencies, e notifierservice.Event) {
	if deps.Notifier == nil {
		return
	}
	_ = deps.Notifier.Notify(ctx, e)
}

// listNotificationsHandler returns the current user's notifications.
func listNotificationsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no_identity"})
			return
		}
		if deps.Notifier == nil {
			writeJSON(w, http.StatusOK, map[string]any{"notifications": []any{}, "unread": 0})
			return
		}
		// We reach into the underlying repos via the notifier's fields.
		inbox := notifierInbox(deps)
		if inbox == nil {
			writeJSON(w, http.StatusOK, map[string]any{"notifications": []any{}, "unread": 0})
			return
		}
		limit := parseLimitParam(r.URL.Query().Get("limit"), 50, 200)
		list, err := inbox.ListByUser(r.Context(), id.UserID, limit)
		if err != nil {
			writeError(w, err)
			return
		}
		unread, _ := inbox.UnreadCount(r.Context(), id.UserID)
		writeJSON(w, http.StatusOK, map[string]any{
			"notifications": list,
			"unread":        unread,
		})
	}
}

// markNotificationReadHandler marks one notification read.
func markNotificationReadHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inbox := notifierInbox(deps)
		if inbox == nil {
			http.Error(w, "notifier not wired", http.StatusServiceUnavailable)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/notifications/")
		id = strings.TrimSuffix(id, "/read")
		if err := inbox.MarkRead(r.Context(), id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// notifierInbox extracts the InboxRepository from the notifier service.
//
// The Notifier field on Dependencies is the concrete notifier.Service so
// we can access its Inbox without re-defining the interface here.
func notifierInbox(deps Dependencies) notifierservice.InboxRepository {
	if deps.Notifier == nil {
		return nil
	}
	return deps.Notifier.Inbox
}

// notifyTaskAssignee sends a notifier event to the project owner about a
// task lifecycle change (claimed/released/submitted). Resolves the agent
// name (if any) and the recipient from deps. Best-effort — never blocks
// the HTTP response, never returns an error.
//
// Phase 16: tasks with project_id IS NULL (Inbox) have no project
// owner to look up. For those we fall back to "first non-system user
// in the database" — the lone owner in a single-user install. If
// there are no users (fresh DB before bootstrap), the call is a no-op
// (which is fine — there's nobody to notify yet).
//
// Returns silently if any lookup fails: notifications must not break
// the underlying claim/release/submit flow.
func notifyTaskAssignee(
	ctx context.Context,
	deps Dependencies,
	eventType, dedupKey string,
	tr *task.Task,
	agentID string,
) {
	if deps.Notifier == nil || tr == nil {
		return
	}
	userID := ""
	if tr.ProjectID != "" {
		p, err := deps.Projects.GetProject(ctx, tr.ProjectID)
		if err == nil && p != nil {
			userID = p.OwnerID
		}
	}
	if userID == "" {
		userID = firstNonSystemUserID(ctx, deps)
	}
	if userID == "" {
		return
	}
	notifyEvent(ctx, deps, notifierservice.Event{
		Type:       eventType,
		UserID:     userID,
		TargetType: "task",
		TargetID:   tr.ID,
		Title:      tr.Title,
		Body:       eventBodyFor(eventType, agentNameOf(ctx, deps, agentID), tr.Title),
		Link:       "/tasks/" + tr.ID,
		DedupKey:   dedupKey,
	})
}

// firstNonSystemUserID finds the first user with role != "system".
// Used as the notification recipient for Inbox (project-less) tasks
// — there is no project owner to consult in that case. The query is
// cheap (one indexed lookup, no full scan thanks to the role column)
// and the call site is a best-effort notifier, so a non-deterministic
// "first" is acceptable.
func firstNonSystemUserID(ctx context.Context, deps Dependencies) string {
	if deps.Users == nil {
		return ""
	}
	u, err := deps.Users.FirstNonSystem(ctx)
	if err != nil || u == nil {
		return ""
	}
	return u.ID
}

// agentNameOf best-effort lookup; returns "" if the agent isn't found.
func agentNameOf(ctx context.Context, deps Dependencies, agentID string) string {
	if agentID == "" || deps.Agents == nil {
		return ""
	}
	a, err := deps.Agents.GetByID(ctx, agentID)
	if err != nil || a == nil {
		return ""
	}
	return a.Name
}

// eventBodyFor returns a short human description for the given event.
func eventBodyFor(eventType, agentName, taskTitle string) string {
	switch eventType {
	case "task.assigned_to_me":
		if agentName != "" {
			return agentName + " picked up: " + taskTitle
		}
		return "Picked up: " + taskTitle
	case "task.released":
		if agentName != "" {
			return agentName + " released: " + taskTitle
		}
		return "Released: " + taskTitle
	default:
		return taskTitle
	}
}

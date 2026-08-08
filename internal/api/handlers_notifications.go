// Package api — notification endpoints + helpers.
package api

import (
	"context"
	"net/http"
	"strings"

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

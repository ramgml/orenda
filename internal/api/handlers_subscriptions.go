// Package api — Phase 10 handlers: bot subscriptions management.
package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
)

// subscriptionInput is the JSON body for POST /notifications/subscriptions.
type subscriptionInput struct {
	BotType       string   `json:"bot_type"`
	TargetAddress string   `json:"target_address"`
	Events        []string `json:"events"`
	Enabled       bool     `json:"enabled"`
}

// subscriptionWriter is satisfied by the sqlite repo (its concrete type
// has Create/Delete beyond the read-only SubscriptionRepository iface).
type subscriptionWriter interface {
	Create(ctx context.Context, s *notifierservice.Subscription) error
	Delete(ctx context.Context, id string) error
}

// listSubscriptionsHandler returns the current user's subscriptions.
func listSubscriptionsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no_identity"})
			return
		}
		if deps.Notifier == nil || deps.Notifier.Subscriptions == nil {
			writeJSON(w, http.StatusOK, map[string]any{"subscriptions": []any{}})
			return
		}
		subs, err := deps.Notifier.Subscriptions.ListForUser(r.Context(), id.UserID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subscriptions": subs})
	}
}

// createSubscriptionHandler adds a subscription.
func createSubscriptionHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no_identity"})
			return
		}
		if deps.Notifier == nil {
			http.Error(w, "notifier not wired", http.StatusServiceUnavailable)
			return
		}
		wr, ok := deps.Notifier.Subscriptions.(subscriptionWriter)
		if !ok {
			http.Error(w, "subscription writes not wired", http.StatusServiceUnavailable)
			return
		}
		var in subscriptionInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.BotType == "" || in.TargetAddress == "" || len(in.Events) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
			return
		}
		s := &notifierservice.Subscription{
			UserID:        id.UserID,
			BotType:       in.BotType,
			TargetAddress: in.TargetAddress,
			Events:        in.Events,
			Enabled:       in.Enabled,
		}
		if err := wr.Create(r.Context(), s); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, s)
	}
}

// deleteSubscriptionHandler removes a subscription.
func deleteSubscriptionHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Notifier == nil {
			http.Error(w, "notifier not wired", http.StatusServiceUnavailable)
			return
		}
		wr, ok := deps.Notifier.Subscriptions.(subscriptionWriter)
		if !ok {
			http.Error(w, "subscription writes not wired", http.StatusServiceUnavailable)
			return
		}
		if err := wr.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

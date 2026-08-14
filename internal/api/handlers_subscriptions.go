// Package api — Phase 10 handlers: bot subscriptions management.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/bot"
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
func listSubscriptionsHandler(deps *Dependencies) http.HandlerFunc {
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
func createSubscriptionHandler(deps *Dependencies) http.HandlerFunc {
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

// BindCodesSource is the narrow seam the Telegram-bind endpoint
// uses to resolve a one-shot onboarding code into a chat id.
// Phase 22.3 follow-up — the Telegram bot stores codes in
// `internal/bot.bindCodes`; this interface lets the API layer
// reach in without importing the concrete bot.
type BindCodesSource interface {
	Consume(code string) (bot.BindCode, error)
}

// telegramBindRequest is the body of POST /bots/telegram/bind.
type telegramBindRequest struct {
	Code string `json:"code"`
}

// telegramBindResponse is the success shape — we return the
// resolved chat id so the UI can show "bound to chat 12345"
// immediately, and the new subscription id so the table refreshes.
type telegramBindResponse struct {
	ChatID         int64  `json:"chat_id"`
	Username       string `json:"username,omitempty"`
	SubscriptionID string `json:"subscription_id"`
}

// telegramBindHandler resolves a `/start`-issued code into a chat
// id and creates a default Telegram subscription for the current
// user. Events default to the same set the manual "Add
// subscription" form offers, so the user gets the same defaults
// either way.
func telegramBindHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no_identity"})
			return
		}
		if deps.BotBindCodes == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "telegram_bot_not_running",
				"hint":  "Telegram bot isn't running — set the token in data/config.yaml and restart.",
			})
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
		var in telegramBindRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Code == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_code"})
			return
		}
		// Consume atomically — second submit of the same code 404s.
		bc, err := deps.BotBindCodes.Consume(in.Code)
		if err != nil {
			switch {
			case errors.Is(err, bot.ErrBindCodeExpired):
				writeJSON(w, http.StatusGone, map[string]string{"error": "code_expired"})
			default:
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "code_unknown"})
			}
			return
		}
		// Default event set — same as the manual "Add
		// subscription" form for Telegram. The user can edit the
		// row later to trim it.
		s := &notifierservice.Subscription{
			UserID:        id.UserID,
			BotType:       "telegram",
			TargetAddress: strconv.FormatInt(bc.ChatID, 10),
			Events: []string{
				"task.review_needed",
				"task.assigned_to_me",
				"mention.created",
				"task.commented",
			},
			Enabled: true,
		}
		if err := wr.Create(r.Context(), s); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, telegramBindResponse{
			ChatID:         bc.ChatID,
			Username:       bc.Username,
			SubscriptionID: s.ID,
		})
	}
}

// deleteSubscriptionHandler removes a subscription.
func deleteSubscriptionHandler(deps *Dependencies) http.HandlerFunc {
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

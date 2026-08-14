// Package api — Phase 10 handlers: bot subscriptions management.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// testBotRequest is the body for POST /bots/test.
//
// Phase 10 (Test send UI): lets the operator push a one-off
// message through any configured bot without first creating a
// subscription. Used from Settings → Bots to verify the bot
// credentials are wired correctly before the user wires up a real
// subscription.
type testBotRequest struct {
	BotType       string `json:"bot_type"`
	TargetAddress string `json:"target_address"`
}

// knownTestBotTypes lists the bot types we accept from the test-send
// UI. "console" is intentionally absent — it writes to server stderr
// and has no user-facing signal, so a "test send" through it would
// look like a silent failure. Keep in sync with BOT_TYPES in the
// web/src/features/settings/Bots.tsx dropdown.
var knownTestBotTypes = map[string]bool{
	"webhook":  true,
	"email":    true,
	"telegram": true,
	"vk":       true,
}

// testBotHandler delivers a single test message through the named
// bot. The handler is deliberately independent of the subscription
// store — the address is whatever the user types in the form, even
// if no subscription exists yet.
//
// Status codes:
//   - 200 + {ok:true, bot_type, target} on success
//   - 400 invalid_input            (missing bot_type or target_address)
//   - 400 unknown_bot_type         (not in knownTestBotTypes)
//   - 503 bot_not_running          (bot not registered in the live registry)
//   - 502 send_failed (+ detail)   (transport-level error after registry hit)
func testBotHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.BotRegistry == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "bot_registry_not_wired",
				"hint":  "Bot registry is not wired in this build of the server.",
			})
			return
		}
		var in testBotRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.BotType == "" || in.TargetAddress == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid_input",
				"hint":  "bot_type and target_address are required.",
			})
			return
		}
		if !knownTestBotTypes[in.BotType] {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "unknown_bot_type",
				"hint":  "Use one of: webhook, email, telegram, vk.",
			})
			return
		}
		botInst := deps.BotRegistry.Get(in.BotType)
		if botInst == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "bot_not_running",
				"hint":  "Bot " + in.BotType + " is not running. Set its credentials in data/config.yaml and restart the server.",
			})
			return
		}
		// Per-bot target sanity check. The transport itself also
		// validates (e.g. webhook URL parse, chat id numeric), but a
		// cheap pre-check surfaces the most common typo in the UI
		// without burning a real network round-trip.
		if msg := validateTestTarget(in.BotType, in.TargetAddress); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		msg := bot.Message{
			Title:  "Orenda test message",
			Body:   "If you got this, your bot is configured correctly. To silence these, remove the matching subscription in Settings → Bots.",
			Kind:   "test",
			Target: in.TargetAddress,
		}
		if err := botInst.Send(r.Context(), in.TargetAddress, msg); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "send_failed",
				"hint":  err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"bot_type": in.BotType,
			"target":   in.TargetAddress,
			"sentinel": "If you got this, your bot is configured correctly.",
			"sent_at":  time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// validateTestTarget returns a non-empty error key when the target
// looks wrong for the given bot type. Empty return means "looks fine
// enough to send". The bot's own Send method is still the source of
// truth — this is a UX pre-filter, not a security boundary.
func validateTestTarget(botType, target string) string {
	switch botType {
	case "webhook":
		// Must be http(s). Don't probe reachability here — the
		// webhook bot's own timeout (10s) is the right place for that.
		if !(strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")) {
			return "webhook_target_must_be_http_url"
		}
	case "email":
		// Loose check: at least an @ and a dot. The SMTP transport
		// will surface protocol-level errors.
		at := strings.IndexByte(target, '@')
		if at < 0 || at == len(target)-1 || !strings.Contains(target[at+1:], ".") {
			return "email_target_must_contain_at_and_dot"
		}
	case "telegram":
		// Telegram chat ids are positive integers (group/channel ids
		// are negative, but a user-typed test target is almost
		// always a 1:1 chat). Reject empty / non-digits here.
		if target == "" {
			return "telegram_target_required"
		}
		for _, r := range target {
			if r < '0' || r > '9' {
				// A leading "-" is allowed for groups/channels.
				if r != '-' {
					return "telegram_target_must_be_numeric"
				}
			}
		}
	case "vk":
		// VK peer ids are positive integers.
		if target == "" {
			return "vk_target_required"
		}
		for _, r := range target {
			if r < '0' || r > '9' {
				return "vk_target_must_be_numeric"
			}
		}
	}
	return ""
}

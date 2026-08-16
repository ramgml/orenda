// Package bot — Telegram transport (Phase 10.5).
//
// Long-polls the Bot API for incoming messages (so /api/v1/agent/bots/telegram/callback
// isn't needed — Telegram callbacks route through the long-poll loop and
// are dispatched by the callback handler in callback.go).
package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Telegram is a Bot that talks to the Telegram Bot API.
type Telegram struct {
	Token string

	// PollTimeout for long-polling.
	PollTimeout time.Duration

	api    *tgbotapi.BotAPI
	stopCh chan struct{}

	// BindCodes holds one-shot onboarding tokens (Phase 22.3
	// follow-up). Initialised in NewTelegram; tests can swap it
	// out before Start().
	BindCodes *bindCodes

	// OnCallback is invoked for every incoming callback_query. The
	// callback handler in callback.go wires this to task actions.
	OnCallback func(ctx context.Context, query CallbackQuery) error

	// OnMessage is invoked for every inbound text message that has
	// no callback_query attached. Phase 21 wires this to the inbox
	// capture flow: "send a line, get a task".
	OnMessage func(ctx context.Context, m InboxMessage) error

	// apiFactory allows tests to inject a fake BotAPI.
	apiFactory func(token string) (telegramAPI, error)
}

// InboxMessage is the inbound text-message payload Phase 21 cares
// about: chat id, sender id, the message text. We deliberately
// keep this smaller than the Telegram update struct so the OnMessage
// callback doesn't pull the whole API surface into its signature.
type InboxMessage struct {
	ChatID    int64
	MessageID int
	UserID    int64 // sender (may differ from chatID for groups)
	Text      string
}

// BindCode is the one-shot token Telegram sends in response to
// `/start`. The user pastes it into Settings → Bots → Telegram to
// link a chat_id to their owner row (Phase 22.3 follow-up).
//
// Codes expire so a leaked screenshot doesn't permanently bind a
// stranger's chat. 10 minutes is long enough to read the message
// and switch windows, short enough that a forgotten tab can't be
// exploited tomorrow.
type BindCode struct {
	Code     string
	ChatID   int64
	Username string // for a friendly greeting; optional
	Expires  time.Time
}

// BindCodeTTL is how long a freshly-issued code stays valid.
const BindCodeTTL = 10 * time.Minute

// bindCodes holds every active code. The mutex keeps concurrent
// /start handlers from clobbering each other; map values carry
// their own expiry so a lazy cleanup on each Consume() keeps the
// map small without a separate timer goroutine.
type bindCodes struct {
	mu    sync.Mutex
	codes map[string]BindCode
}

// newBindCodes returns an empty in-memory store.
func newBindCodes() *bindCodes {
	return &bindCodes{codes: map[string]BindCode{}}
}

// Issue creates a new code for chatID. The code is 6 hex chars
// (24 bits) — enough entropy for a single-user install and easy
// enough to type from a phone.
func (b *bindCodes) Issue(chatID int64, username string) BindCode {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw := make([]byte, 3)
	_, _ = rand.Read(raw)
	c := BindCode{
		Code:     strings.ToUpper(hex.EncodeToString(raw)),
		ChatID:   chatID,
		Username: username,
		Expires:  time.Now().Add(BindCodeTTL),
	}
	b.codes[c.Code] = c
	return c
}

// Consume looks up a code and removes it atomically. Returns the
// (now-removed) entry or ErrBindCodeUnknown if the code wasn't
// issued or already used. Expired codes are also evicted here so
// we don't need a background timer.
func (b *bindCodes) Consume(code string) (BindCode, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.codes[code]
	if !ok {
		return BindCode{}, ErrBindCodeUnknown
	}
	delete(b.codes, code)
	if time.Now().After(c.Expires) {
		return BindCode{}, ErrBindCodeExpired
	}
	return c, nil
}

// ErrBindCodeUnknown / ErrBindCodeExpired are returned by
// Consume() so the API layer can surface a clean 404 vs a 410.
var (
	ErrBindCodeUnknown = fmt.Errorf("bot: bind code not found")
	ErrBindCodeExpired = fmt.Errorf("bot: bind code expired")
)

// SendReply is a tiny helper to reply to the chat from inside the
// OnMessage handler. It doesn't depend on any extra wiring — the
// Telegram bot's `api` field is reachable through a closure on
// the receiver. See poll() below for how OnMessage is called.
func (t *Telegram) SendReply(ctx context.Context, chatID int64, text string) error {
	if t.api == nil {
		return ErrBotUnavailable
	}
	msg := tgbotapi.NewMessage(chatID, text)
	_, err := t.api.Send(msg)
	return err
}

// telegramAPI is the tiny surface the bot needs (lets tests mock).
type telegramAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	GetUpdatesChan(u tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel
}

// CallbackQuery mirrors a Telegram callback_query for the callback handler.
type CallbackQuery struct {
	ID        string
	ChatID    int64
	MessageID int
	Data      string // e.g. "approve:task:019fe..."
}

// NewTelegram returns a Telegram bot.
func NewTelegram(token string) *Telegram {
	return &Telegram{
		Token:       token,
		PollTimeout: 30 * time.Second,
		stopCh:      make(chan struct{}),
		BindCodes:   newBindCodes(),
	}
}

// Name implements Bot.
func (Telegram) Name() string { return "telegram" }

// Start begins long-polling in a goroutine. Returns nil when polling
// started; the goroutine runs until Stop is called.
func (t *Telegram) Start(ctx context.Context) error {
	if t.Token == "" {
		return ErrBotUnavailable
	}
	factory := t.apiFactory
	if factory == nil {
		factory = func(token string) (telegramAPI, error) {
			return tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, &http.Client{Timeout: 30 * time.Second})
		}
	}
	api, err := factory(t.Token)
	if err != nil {
		return fmt.Errorf("telegram: api: %w", err)
	}
	t.api = api.(*tgbotapi.BotAPI)

	go t.poll(ctx)
	return nil
}

// Stop shuts down the polling loop.
func (t *Telegram) Stop(ctx context.Context) error {
	close(t.stopCh)
	return nil
}

// Send delivers the message to a chat. target is the chat id (string).
//
// Renders msg.Actions as an inline keyboard (one row, all buttons).
// Falls back to the legacy hard-coded review-needed buttons when
// Actions is empty AND the event is task.review_needed — keeps the
// old behaviour for callers that haven't been migrated to Actions.
func (t *Telegram) Send(ctx context.Context, target string, msg Message) error {
	if t.api == nil {
		return ErrBotUnavailable
	}
	if target == "" {
		return ErrTargetMissing
	}
	var chatID int64
	if _, err := fmt.Sscanf(target, "%d", &chatID); err != nil {
		return fmt.Errorf("telegram: bad chat id %q: %w", target, err)
	}

	text := msg.Title + "\n\n" + msg.Body
	if msg.Link != "" {
		text += "\n\n" + msg.Link
	}
	mc := tgbotapi.NewMessage(chatID, text)
	mc.ParseMode = tgbotapi.ModeMarkdown

	if buttons := actionsToTelegram(msg); len(buttons) > 0 {
		mc.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(buttons...))
	} else if msg.Kind == "task.review_needed" && msg.CallbackID != "" {
		// Legacy fallback so old notifier callers without Actions still
		// ship a usable message.
		mc.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ Approve", "approve:"+msg.CallbackID),
				tgbotapi.NewInlineKeyboardButtonData("↩️ Reject", "reject:"+msg.CallbackID),
			),
		)
	}

	_, err := t.api.Send(mc)
	if err != nil {
		return fmt.Errorf("telegram: send: %w", err)
	}
	return nil
}

// actionsToTelegram maps Message.Actions onto a single Telegram row.
// URL actions render as URL buttons; everything else as callback_data
// keyed on the action verb + the message's CallbackID.
func actionsToTelegram(msg Message) []tgbotapi.InlineKeyboardButton {
	if len(msg.Actions) == 0 {
		return nil
	}
	row := make([]tgbotapi.InlineKeyboardButton, 0, len(msg.Actions))
	for _, a := range msg.Actions {
		if a.URL != "" {
			row = append(row, tgbotapi.NewInlineKeyboardButtonURL(a.Label, a.URL))
			continue
		}
		// CallbackID is the task id (or empty); the wire payload is
		// "<verb>:<callbackID>". Empty CallbackID falls back to Target
		// so old callers without Actions still produce a stable nonce.
		id := msg.CallbackID
		if id == "" {
			id = msg.Target
		}
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(a.Label, a.Callback+":"+id))
	}
	if len(row) == 0 {
		return nil
	}
	return row
}

// handleStart generates a fresh bind code for the chat that sent
// /start and replies with it. The user copies the code into the
// UI to link this Telegram chat to their owner row.
//
// We always reply even if the bot is mid-restart so the user
// sees a single coherent message ("here's your code"); a failed
// reply is swallowed — there's no recovery action the user can
// take from inside Telegram.
func (t *Telegram) handleStart(ctx context.Context, m *tgbotapi.Message) {
	username := ""
	if m.From != nil {
		username = m.From.UserName
	}
	c := t.BindCodes.Issue(m.Chat.ID, username)
	text := fmt.Sprintf(
		"Welcome to Orenda.\n\nYour one-time binding code is:\n\n  %s\n\n"+
			"Open Settings → Bots → Telegram in the app and paste the code there. "+
			"The code expires in %s and can only be used once.",
		c.Code, BindCodeTTL,
	)
	if err := t.SendReply(ctx, m.Chat.ID, text); err != nil {
		// No logger wired into the bot; main.go is responsible for
		// surfacing errors. The chat just won't see a reply — they
		// can retry /start.
		_ = err
	}
}

// poll runs the long-polling loop.
func (t *Telegram) poll(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = int(t.PollTimeout.Seconds())
	updates := t.api.GetUpdatesChan(u)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stopCh:
			return
		case upd, ok := <-updates:
			if !ok {
				return
			}
			if upd.CallbackQuery != nil && t.OnCallback != nil {
				q := upd.CallbackQuery
				chatID := int64(0)
				msgID := 0
				if q.Message != nil {
					chatID = q.Message.Chat.ID
					msgID = q.Message.MessageID
				}
				_ = t.OnCallback(ctx, CallbackQuery{
					ID:        q.ID,
					ChatID:    chatID,
					MessageID: msgID,
					Data:      q.Data,
				})
				continue
			}
			// Plain text message — Phase 21 inbox capture. We only
			// dispatch to OnMessage if the chat type is private (a
			// group message would route to a multi-user codepath we
			// haven't built yet — single-owner install ignores
			// non-private chats by design).
			if upd.Message != nil && t.OnMessage != nil {
				m := upd.Message
				if m.Chat.Type == "private" && strings.TrimSpace(m.Text) != "" {
					// /start is the bind handshake. Reply with a
					// fresh one-shot code so the user can paste it
					// into Settings → Bots → Telegram and link this
					// chat to their owner row.
					if strings.HasPrefix(strings.TrimSpace(m.Text), "/start") {
						t.handleStart(ctx, m)
						continue
					}
					userID := int64(0)
					if m.From != nil {
						userID = m.From.ID
					}
					_ = t.OnMessage(ctx, InboxMessage{
						ChatID:    m.Chat.ID,
						MessageID: m.MessageID,
						UserID:    userID,
						Text:      m.Text,
					})
				}
			}
		}
	}
}

// AnswerCallback acknowledges a callback (removes the loading spinner).
func (t *Telegram) AnswerCallback(_ context.Context, id, text string) error {
	if t.api == nil {
		return ErrBotUnavailable
	}
	cb := tgbotapi.NewCallback(id, text)
	_, err := t.api.Send(cb)
	return err
}

// ParseCallbackData splits "action:task_id" into its parts.
func ParseCallbackData(data string) (action, target string, err error) {
	parts := strings.SplitN(data, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("bot: bad callback data %q", data)
	}
	return parts[0], parts[1], nil
}

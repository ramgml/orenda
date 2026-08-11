// Package bot — Telegram transport (Phase 10.5).
//
// Long-polls the Bot API for incoming messages (so /api/v1/agent/bots/telegram/callback
// isn't needed — Telegram callbacks route through the long-poll loop and
// are dispatched by the callback handler in callback.go).
package bot

import (
	"context"
	"fmt"
	"net/http"
	"strings"
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

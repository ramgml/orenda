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

	// apiFactory allows tests to inject a fake BotAPI.
	apiFactory func(token string) (telegramAPI, error)
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

	// Interactive buttons for review events (Phase 10.5).
	if msg.Kind == "task.review_needed" && msg.Target != "" {
		approve := tgbotapi.NewInlineKeyboardButtonData("✅ Approve", "approve:"+msg.Target)
		reject := tgbotapi.NewInlineKeyboardButtonData("↩️ Reject", "reject:"+msg.Target)
		mc.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(approve, reject),
		)
	}

	_, err := t.api.Send(mc)
	if err != nil {
		return fmt.Errorf("telegram: send: %w", err)
	}
	return nil
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

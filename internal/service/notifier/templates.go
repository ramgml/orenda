// Package notifier — per-event-type Message templates.
//
// Each template renders an Event into a bot.Message, attaching the
// right Action buttons for transports that render them (VK, Telegram).
// Transports without inline-button support (console, webhook, email)
// ignore the Actions slice — the title/body still make sense on their own.
//
// Adding a new event kind: implement Template and register in Default.
//
// This file is the single place that decides what each notification
// looks like across every bot, which is the whole point of moving
// away from the previous "one title/body for everything" design.
package notifier

import (
	"github.com/ramgml/orenda/internal/bot"
)

// Template renders an Event into a bot-ready Message.
//
// The Message's Target field MUST be the task id (or comparable id)
// so callback handlers can correlate button presses back to the
// originating notification. For events without an action target
// (e.g. backup.failed) Target may be empty — bots render plain text.
type Template func(e Event) bot.Message

// ---------------------------------------------------------------------------
// task.review_needed — Approve / Reject
// ---------------------------------------------------------------------------

// ReviewNeeded adds Approve/Reject buttons pointing at the task id.
// Used when an agent submits a task and the owner needs to decide.
func ReviewNeeded(e Event) bot.Message {
	return bot.Message{
		Kind:       "task.review_needed",
		Title:      e.Title,
		Body:       e.Body,
		Target:     e.TargetID, // overwritten by notifier with recipient address
		CallbackID: e.TargetID, // stable id the bot uses for callback payloads
		Link:       e.Link,
		Actions: []bot.Action{
			{Label: "✅ Approve", Callback: "approve"},
			{Label: "↩️ Reject", Callback: "reject"},
		},
	}
}

// ---------------------------------------------------------------------------
// task.assigned_to_me / task.released — link to the task
// ---------------------------------------------------------------------------

// AssignedToMe renders a one-line "open the task" message. URL-based
// because there's nothing to decide yet.
func AssignedToMe(e Event) bot.Message {
	return bot.Message{
		Kind:       "task.assigned_to_me",
		Title:      e.Title,
		Body:       e.Body,
		Target:     e.TargetID,
		CallbackID: e.TargetID,
		Link:       e.Link,
		Actions: []bot.Action{
			{Label: "Open task", URL: e.Link},
		},
	}
}

// Released is identical in shape to AssignedToMe but uses a separate
// template so future tweaks (e.g. "re-open claim" button) don't bleed
// into the pick-up flow. We override the Kind so transports can
// branch if they want to.
func Released(e Event) bot.Message {
	m := AssignedToMe(e)
	m.Kind = "task.released"
	return m
}

// ---------------------------------------------------------------------------
// mention.created / task.commented / agent.offline / backup.failed —
// plain text, no actions
// ---------------------------------------------------------------------------

// Plain is the catch-all template for events that don't carry an
// action: mentions, comments, agent offline status, backup failures.
//
// Body already lives on e.Body (notifier.Notify uses it verbatim), so
// this template mainly sets the right Kind so transports can branch
// on it if they want to.
func Plain(e Event) bot.Message {
	return bot.Message{
		Kind:       e.Type,
		Title:      e.Title,
		Body:       e.Body,
		Target:     e.TargetID,
		CallbackID: e.TargetID,
		Link:       e.Link,
	}
}

// ---------------------------------------------------------------------------
// Default registry
// ---------------------------------------------------------------------------

// Default returns the standard template table. Wire it into the
// notifier.Service at startup so callers don't have to know which
// template fits which event.
var Default = map[string]Template{
	"task.review_needed":  ReviewNeeded,
	"task.assigned_to_me": AssignedToMe,
	"task.released":       Released,
	"mention.created":     Plain,
	"task.commented":      Plain,
	"agent.offline":       Plain,
	"backup.failed":       Plain,
	"event.upcoming_1h":   Plain,
	// Phase 30.5: weekly digest. The scheduler assembles a
	// DigestStats snapshot and stuffs the rendered Message into
	// Event.Meta["rendered"] via Plain-style template — we don't
	// recompute here; the scheduler did it once with the right
	// repo handles. See internal/service/notifier/digest.go for
	// the formatter and cmd/orenda/main.go for the scheduler.
	"digest.weekly": WeeklyDigestFromEvent,
}

// WeeklyDigestFromEvent is the template adapter for digest.weekly.
// The scheduler pre-renders the message via RenderWeeklyDigest and
// stuffs it into Event.Meta["body"]; this template just rebuilds a
// Message with the right Kind/Target/Link so transports handle it
// like any other notification.
//
// We deliberately don't recompute stats inside the template —
// pulling all those repos into the template would force the
// notifier package to depend on storage/sqlite, which is a
// layering violation. The scheduler is the only place that owns
// the data flow.
func WeeklyDigestFromEvent(e Event) bot.Message {
	body := e.Meta["body"]
	title := e.Meta["title"]
	if title == "" {
		title = "Weekly digest"
	}
	return bot.Message{
		Kind:       "digest.weekly",
		Title:      title,
		Body:       body,
		Target:     e.UserID,
		CallbackID: "",
		Link:       e.Link,
	}
}

// Render is a convenience used by the notifier.Service: looks up the
// template for e.Type and applies it. Unknown event types fall back
// to Plain so adding new kinds is safe.
func Render(e Event) bot.Message {
	if t, ok := Default[e.Type]; ok {
		return t(e)
	}
	return Plain(e)
}

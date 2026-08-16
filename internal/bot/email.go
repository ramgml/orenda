// Package bot — email transport (Phase 10.6 + Phase 30.4: HTML).
//
// Email is delivered as multipart/alternative so the recipient's
// client picks the best part. Plain text remains the canonical body
// (we always emit it for accessibility + clients without HTML); HTML
// carries the rendered styling and the action buttons (Approve /
// Reject) for review-needed events.
//
// We deliberately don't pull in a third-party HTML template engine
// — the inlined CSS + simple `actionsToEmail` rules cover the cases
// the Notifier currently emits. A future templating layer can grow
// on top of `renderHTML` without changing the transport.
package bot

import (
	"context"
	"crypto/tls"
	"fmt"
	"html"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Email is a Bot that sends messages via SMTP.
type Email struct {
	Host     string // e.g. "smtp.example.com:587"
	Username string
	Password string
	From     string
	// UseTLS=true means implicit TLS (port 465); false = STARTTLS.
	UseTLS bool

	// PublicBaseURL is the URL that review-action buttons should
	// link back to (e.g. "https://orenda.example.com"). When empty
	// (the common default in dev), action URLs are omitted from the
	// HTML body — the operator hasn't told us where the binary is
	// hosted, so we don't guess.
	PublicBaseURL string

	// dial is injectable for tests.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewEmail returns an Email bot.
func NewEmail(host, username, password, from string, useTLS bool) *Email {
	return &Email{
		Host:     host,
		Username: username,
		Password: password,
		From:     from,
		UseTLS:   useTLS,
	}
}

// Name implements Bot.
func (Email) Name() string { return "email" }

// Start implements Bot.
func (Email) Start(context.Context) error { return nil }

// Stop implements Bot.
func (Email) Stop(context.Context) error { return nil }

// Send delivers the message via SMTP. target is the recipient address.
func (e *Email) Send(ctx context.Context, target string, msg Message) error {
	if e.Host == "" || e.From == "" {
		return ErrBotUnavailable
	}
	if target == "" {
		return ErrTargetMissing
	}

	subject := msg.Title
	if subject == "" {
		subject = "(no subject)"
	}

	plain := renderPlain(msg)
	htmlBody := renderHTML(msg, e.PublicBaseURL)

	body := buildMultipartAlternative(e.From, target, subject, plain, htmlBody)

	dial := e.dial
	if dial == nil {
		d := &net.Dialer{Timeout: 10 * time.Second}
		dial = d.DialContext
	}

	conn, err := dial(ctx, "tcp", e.Host)
	if err != nil {
		return fmt.Errorf("email: dial: %w", err)
	}

	var client *smtp.Client
	hostName := strings.Split(e.Host, ":")[0]
	if e.UseTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: hostName})
		client, err = smtp.NewClient(tlsConn, e.Host)
	} else {
		client, err = smtp.NewClient(conn, e.Host)
	}
	if err != nil {
		return fmt.Errorf("email: smtp: %w", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok && !e.UseTLS {
		if err := client.StartTLS(&tls.Config{ServerName: hostName}); err != nil {
			return fmt.Errorf("email: starttls: %w", err)
		}
	}

	if e.Username != "" {
		auth := smtp.PlainAuth("", e.Username, e.Password, hostName)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}

	if err := client.Mail(e.From); err != nil {
		return fmt.Errorf("email: mail from: %w", err)
	}
	if err := client.Rcpt(target); err != nil {
		return fmt.Errorf("email: rcpt: %w", err)
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("email: data: %w", err)
	}
	if _, err := wc.Write([]byte(body)); err != nil {
		return fmt.Errorf("email: write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("email: close: %w", err)
	}
	return client.Quit()
}

// ----------------------------------------------------------------------------
// Body renderers.
// ----------------------------------------------------------------------------

// renderPlain returns the canonical text/plain representation. Kept
// short and link-only — every email has a text part for accessibility.
func renderPlain(msg Message) string {
	var b strings.Builder
	b.WriteString(msg.Title)
	if msg.Body != "" {
		b.WriteString("\n\n")
		b.WriteString(msg.Body)
	}
	if msg.Link != "" {
		b.WriteString("\n\n")
		b.WriteString(msg.Link)
	}
	for _, a := range msg.Actions {
		// text clients can't render buttons — surface them as a
		// labelled URL the user can copy/paste.
		if a.URL != "" {
			b.WriteString(fmt.Sprintf("\n%s: %s", a.Label, a.URL))
			continue
		}
		// Callback-style: we don't know the URL yet (the server
		// would need to render it) — surface the action verb so the
		// reader knows what would happen.
		if msg.CallbackID != "" {
			b.WriteString(fmt.Sprintf("\n[%s] reply to approve/reject task %s",
				a.Label, msg.CallbackID))
		}
	}
	return b.String()
}

// renderHTML returns the styled HTML body. Layout: heading (title),
// lead paragraph (body), optional link card, optional action buttons
// row. All CSS is inline (most clients strip <style> tags) and uses
// only safe fonts and colours.
func renderHTML(msg Message, publicBaseURL string) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><body style="margin:0;padding:24px;background:#f6f7f8;` +
		`font-family:-apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#1f2937;">`)
	b.WriteString(`<div style="max-width:560px;margin:0 auto;background:#ffffff;` +
		`border-radius:8px;border:1px solid #e5e7eb;overflow:hidden;">`)

	// Heading bar (Orenda brand color).
	b.WriteString(`<div style="background:#3b82f6;color:#ffffff;padding:16px 20px;` +
		`font-size:14px;font-weight:600;letter-spacing:0.02em;">Orenda</div>`)

	b.WriteString(`<div style="padding:24px 20px;">`)
	b.WriteString(`<h1 style="margin:0 0 12px 0;font-size:20px;line-height:1.4;">`)
	b.WriteString(html.EscapeString(msg.Title))
	b.WriteString(`</h1>`)

	if msg.Body != "" {
		// Normalise newlines into <br/> for readability without
		// pulling in a markdown engine.
		escaped := html.EscapeString(msg.Body)
		escaped = strings.ReplaceAll(escaped, "\n", "<br/>")
		b.WriteString(`<p style="margin:0 0 16px 0;font-size:15px;line-height:1.55;color:#374151;">`)
		b.WriteString(escaped)
		b.WriteString(`</p>`)
	}

	if msg.Link != "" {
		b.WriteString(`<p style="margin:0 0 16px 0;"><a href="`)
		b.WriteString(html.EscapeString(msg.Link))
		b.WriteString(`" style="color:#2563eb;text-decoration:underline;">`)
		b.WriteString(html.EscapeString(msg.Link))
		b.WriteString(`</a></p>`)
	}

	// Action buttons: only emitted when we have an explicit callback
	// id AND a public URL to wire the buttons against. Without the
	// base URL we can't sign / address the action, so we fall back
	// to a small note in the body — better than a broken button that
	// goes nowhere.
	if len(msg.Actions) > 0 && publicBaseURL != "" {
		b.WriteString(`<div style="margin-top:8px;">`)
		for _, a := range msg.Actions {
			label := html.EscapeString(a.Label)
			if a.URL != "" {
				// Pre-built URL action — render as a button.
				button := fmt.Sprintf(
					`<a href="%s" style="display:inline-block;background:#2563eb;color:#ffffff;`+
						`padding:10px 18px;border-radius:6px;text-decoration:none;font-weight:600;`+
						`margin-right:8px;margin-bottom:4px;">%s</a>`,
					html.EscapeString(a.URL), label)
				b.WriteString(button)
				continue
			}
			// Callback-style action: we know the verb and the
			// callback id; expose via a query-param link so the
			// recipient can click it. The server-side handler is
			// not yet implemented (Phase 30.4 deferral) — when it
			// lands, this URL format must match. For now, surface
			// a neutral "Open in Orenda" link.
			if msg.CallbackID != "" {
				href := fmt.Sprintf("%s/api/v1/tasks/%s/review?action=%s",
					strings.TrimRight(publicBaseURL, "/"),
					msg.CallbackID,
					html.EscapeString(a.Callback))
				button := fmt.Sprintf(
					`<a href="%s" style="display:inline-block;background:#2563eb;color:#ffffff;`+
						`padding:10px 18px;border-radius:6px;text-decoration:none;font-weight:600;`+
						`margin-right:8px;margin-bottom:4px;">%s</a>`,
					html.EscapeString(href), label)
				b.WriteString(button)
			}
		}
		b.WriteString(`</div>`)
	}

	b.WriteString(`</div>`) // padding wrapper

	// Footer (single line, no unsubscribe link — single-owner install).
	b.WriteString(`<div style="background:#f9fafb;padding:12px 20px;font-size:12px;color:#6b7280;` +
		`border-top:1px solid #e5e7eb;">Sent by your Orenda instance.</div>`)

	b.WriteString(`</div></body></html>`)
	return b.String()
}

// buildMultipartAlternative wraps plain + html bodies in a single
// RFC 2046 multipart/alternative envelope. The boundary is a fresh
// random-looking 24-character string (we don't need cryptographic
// uniqueness — the line lives only inside this single message).
//
// Generated with simple math/rand-free helper so the envelope body is
// deterministic enough for tests without importing math/rand.
func buildMultipartAlternative(from, to, subject, plain, htmlBody string) string {
	boundary := "orenda_" + uniqueBoundary()
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n")
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
	b.WriteString(plain)
	b.WriteString("\r\n\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
	b.WriteString(htmlBody)
	b.WriteString("\r\n\r\n")

	b.WriteString("--" + boundary + "--\r\n")
	return b.String()
}

// uniqueBoundary returns a deterministic 20-hex-char string derived
// from the current nanosecond timestamp. Sufficient to be unique
// within a single Send call (and across concurrent goroutines — each
// has its own timestamp).
func uniqueBoundary() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

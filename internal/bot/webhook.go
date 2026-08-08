// Package bot — webhook transport (Phase 10.7).
//
// POSTs the message as JSON to the configured URL. When a Secret is set,
// the body is signed with HMAC-SHA256 in the X-Orenda-Signature header so
// receivers can verify authenticity (Phase 10.9 callback verification uses
// the same secret).
package bot

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Webhook is a Bot that POSTs JSON to a URL.
type Webhook struct {
	URL       string
	Secret    string // HMAC key, optional
	Timeout   time.Duration
	UserAgent string

	client *http.Client
}

// NewWebhook returns a Webhook bot.
func NewWebhook(url, secret string) *Webhook {
	return &Webhook{
		URL:       url,
		Secret:    secret,
		Timeout:   10 * time.Second,
		UserAgent: "orenda-bot/0.1",
	}
}

// Name implements Bot.
func (Webhook) Name() string { return "webhook" }

// Start implements Bot (no long-running loop needed).
func (w *Webhook) Start(context.Context) error { return nil }

// Stop implements Bot.
func (w *Webhook) Stop(context.Context) error { return nil }

// Send POSTs the message as JSON. 2xx is success; anything else is a
// retryable error (the notifier will back off and retry).
func (w *Webhook) Send(ctx context.Context, target string, msg Message) error {
	url := w.URL
	if target != "" {
		// The subscription's target_address overrides the static URL so
		// one subscription can fan out to many endpoints.
		url = target
	}
	if url == "" {
		return ErrTargetMissing
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("webhook: marshal: %w", err)
	}

	c := w.client
	if c == nil {
		c = &http.Client{Timeout: w.Timeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", w.UserAgent)
	if w.Secret != "" {
		mac := hmac.New(sha256.New, []byte(w.Secret))
		mac.Write(body)
		req.Header.Set("X-Orenda-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: send: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: %s", resp.Status)
	}
	return nil
}

// VerifySignature checks the HMAC signature for an incoming callback
// body. Used by the callback handler (Phase 10.9).
func VerifySignature(secret string, body []byte, sigHeader string) bool {
	const prefix = "sha256="
	if len(sigHeader) <= len(prefix) || sigHeader[:len(prefix)] != prefix {
		return false
	}
	got, err := hex.DecodeString(sigHeader[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

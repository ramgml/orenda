// Package bot — VK Community bot (Phase 10.4).
//
// VK requires a callback endpoint OR long-poll. Phase 10 ships the
// callback-API flavour: the handler in callback.go verifies the
// confirmation token and processes incoming events; Send uses the
// messages.send API directly.
//
// The full vksdk dependency is intentionally avoided — VK's API is plain
// HTTP + JSON, so we hit it directly and keep the dependency surface
// small.
package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// VK is a Bot that talks to the VK Community Bot API.
type VK struct {
	Token      string
	GroupID    int64
	APIVersion string

	httpClient *http.Client
	baseURL    string // defaults to https://api.vk.com/method
}

// NewVK returns a VK bot.
func NewVK(token string, groupID int64) *VK {
	return &VK{
		Token:      token,
		GroupID:    groupID,
		APIVersion: "5.131",
		baseURL:    "https://api.vk.com/method",
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Name implements Bot.
func (VK) Name() string { return "vk" }

// WithBaseURL overrides the API endpoint (used by tests).
func (v *VK) WithBaseURL(base string) *VK {
	v.baseURL = base
	return v
}

// Start implements Bot (callback mode — no polling).
func (VK) Start(context.Context) error { return nil }

// Stop implements Bot.
func (VK) Stop(context.Context) error { return nil }

// Send delivers a message to a VK peer (target is the peer_id).
func (v *VK) Send(ctx context.Context, target string, msg Message) error {
	if v.Token == "" {
		return ErrBotUnavailable
	}
	if target == "" {
		return ErrTargetMissing
	}

	peerID, err := strconv.ParseInt(target, 10, 64)
	if err != nil {
		return fmt.Errorf("vk: bad peer id %q: %w", target, err)
	}

	text := msg.Title + "\n\n" + msg.Body
	if msg.Link != "" {
		text += "\n\n" + msg.Link
	}

	form := url.Values{
		"access_token": {v.Token},
		"v":            {v.APIVersion},
		"peer_id":      {strconv.FormatInt(peerID, 10)},
		"message":      {text},
		"random_id":    {strconv.FormatInt(time.Now().UnixNano(), 10)},
	}

	// Interactive buttons: prefer msg.Actions, fall back to the
	// legacy task.review_needed keyboard for callers that haven't
	// migrated yet.
	if buttons := actionsToVK(msg); len(buttons) > 0 {
		kb := map[string]any{
			"one_time": false,
			"inline":   true,
			"buttons":  buttons,
		}
		if data, err := json.Marshal(kb); err == nil {
			form.Set("keyboard", string(data))
		}
	} else if msg.Kind == "task.review_needed" && msg.CallbackID != "" {
		kb := map[string]any{
			"one_time": false,
			"inline":   true,
			"buttons": [][]any{
				{
					map[string]any{
						"action": map[string]any{
							"type":    "callback",
							"label":   "✅ Принять",
							"payload": fmt.Sprintf(`{"a":"approve","t":%q}`, msg.CallbackID),
						},
						"color": "positive",
					},
					map[string]any{
						"action": map[string]any{
							"type":    "callback",
							"label":   "↩️ Вернуть",
							"payload": fmt.Sprintf(`{"a":"reject","t":%q}`, msg.CallbackID),
						},
						"color": "negative",
					},
				},
			},
		}
		if data, err := json.Marshal(kb); err == nil {
			form.Set("keyboard", string(data))
		}
	}

	endpoint := v.baseURL + "/messages.send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("vk: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("vk: send: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("vk: %s: %s", resp.Status, string(body))
	}
	var out struct {
		Response any `json:"response"`
		Error    *struct {
			ErrorCode int    `json:"error_code"`
			ErrorMsg  string `json:"error_msg"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("vk: parse: %w", err)
	}
	if out.Error != nil {
		return fmt.Errorf("vk: %d: %s", out.Error.ErrorCode, out.Error.ErrorMsg)
	}
	return nil
}

// actionsToVK maps Message.Actions to a single VK button row. Callback
// payloads are keyed on Action verb + CallbackID (or Target as
// fallback for old callers).
func actionsToVK(msg Message) [][]any {
	if len(msg.Actions) == 0 {
		return nil
	}
	id := msg.CallbackID
	if id == "" {
		id = msg.Target
	}
	row := make([]any, 0, len(msg.Actions))
	for _, a := range msg.Actions {
		if a.URL != "" {
			row = append(row, map[string]any{
				"action": map[string]any{
					"type":  "open_link",
					"label": a.Label,
					"link":  a.URL,
				},
				"color": "primary",
			})
			continue
		}
		row = append(row, map[string]any{
			"action": map[string]any{
				"type":    "callback",
				"label":   a.Label,
				"payload": fmt.Sprintf(`{"a":%q,"t":%q}`, a.Callback, id),
			},
			"color": "primary",
		})
	}
	return [][]any{row}
}

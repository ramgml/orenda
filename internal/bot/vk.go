// Package bot — VK Community bot (Phase 10.4 + Phase 30.3: Long Poll).
//
// VK has two transports for a community bot:
//
//   - Callback API: a public HTTP endpoint the operator hosts. VK POSTs
//     events to it. Available since Phase 10.4 (callback.go).
//   - Long Poll: an outbound HTTPS connection from our server to
//     `vk.com`. No public endpoint needed — works behind NAT / for
//     operators who can't expose a port. Phase 30.3 ships this.
//
// Phase 30.3 implementation focuses on:
//   - groups.getLongPollServer → server, key, ts
//   - a_check loop with `wait=25` (long-poll)
//   - failed=1/2/3/4 recovery (re-fetch server, advance ts, refresh key)
//   - dispatch message_new (type 4) to OnMessage (Phase 21 inbox capture)
//   - graceful shutdown on ctx / Stop
//
// The full vksdk dependency is intentionally avoided — VK's API is plain
// HTTP + JSON, so we hit it directly and keep the dependency surface
// small.
package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// VK is a Bot that talks to the VK Community Bot API.
//
// Wire shape: Send uses messages.send directly. For inbound updates the
// bot can either run in Callback mode (an external endpoint POSTs events
// here, handled by callback.go) or in Long Poll mode (Start launches a
// goroutine that polls the Bot API). Both transports can coexist in
// principle but typically an operator picks one.
type VK struct {
	Token      string
	GroupID    int64
	APIVersion string

	// PollTimeout is the `wait` parameter for a_check. Long-poll holds
	// the connection for up to this many seconds before returning an
	// empty update set. 25s matches the official recommendation.
	PollTimeout time.Duration

	// OnMessage is invoked for every inbound text message that has
	// no callback payload attached. Phase 21 wires this to the inbox
	// capture flow: "send a line, get a task". Symmetric with
	// Telegram's OnMessage so the same main.go inbox wiring applies.
	OnMessage func(ctx context.Context, m InboxMessage) error

	// OnError is invoked for transient poll errors (network failure,
	// bot API rejection, server-rejected ts). Best-effort — used by
	// main.go to log +429s; tests use it for assertions.
	OnError func(err error)

	httpClient *http.Client
	baseURL    string // https://api.vk.com/method

	// Long-poll endpoint scheme. The actual host comes from the
	// groups.getLongPollServer response (a `host:port` value). Tests
	// override this to point to an httptest server.
	lpScheme string

	stopCh chan struct{}
	stopMu sync.Mutex
	closed bool
}

// NewVK returns a VK bot.
func NewVK(token string, groupID int64) *VK {
	return &VK{
		Token:       token,
		GroupID:     groupID,
		APIVersion:  "5.131",
		PollTimeout: 25 * time.Second,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		baseURL:     "https://api.vk.com/method",
		lpScheme:    "https://",
	}
}

// Name implements Bot.
func (v *VK) Name() string { return "vk" }

// WithBaseURL overrides the API endpoint (used by tests).
func (v *VK) WithBaseURL(base string) *VK {
	v.baseURL = base
	return v
}

// WithLongPollScheme overrides the long-poll scheme (used by tests to
// point a_check at an httptest server over plain http).
func (v *VK) WithLongPollScheme(scheme string) *VK {
	v.lpScheme = scheme
	return v
}

// WithHTTPClient overrides the HTTP client. Useful for tests that
// want to inject a custom Transport (e.g., to rewrite a_check URLs).
func (v *VK) WithHTTPClient(c *http.Client) *VK {
	v.httpClient = c
	return v
}

// Start implements Bot. When the group ID is set (the normal case for
// a community bot that doesn't expose a callback endpoint) the bot
// begins long-polling events in a background goroutine. If you want
// Callback-only mode instead, just leave GroupID at 0 — Send will
// still work, the bot just won't receive any inbound messages.
func (v *VK) Start(ctx context.Context) error {
	v.stopMu.Lock()
	if v.closed {
		v.stopMu.Unlock()
		return errors.New("vk: bot already stopped")
	}
	if v.Token == "" {
		v.stopMu.Unlock()
		return ErrBotUnavailable
	}
	v.stopCh = make(chan struct{})
	v.stopMu.Unlock()

	if v.GroupID == 0 {
		// Callback-only mode — no background loop needed.
		return nil
	}
	go v.pollLoop(ctx)
	return nil
}

// Stop implements Bot. Closes the long-poll goroutine. In-flight
// a_check requests honour the request context, so the bot exits
// within PollTimeout of the call.
func (v *VK) Stop(ctx context.Context) error {
	v.stopMu.Lock()
	defer v.stopMu.Unlock()
	if v.closed {
		return nil
	}
	if v.stopCh != nil {
		close(v.stopCh)
	}
	v.closed = true
	return nil
}

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

// ----------------------------------------------------------------------------
// Long Poll transport (Phase 30.3).
// ----------------------------------------------------------------------------

// longPollServer is the handle the Bot API issues for a_check polling.
type longPollServer struct {
	Server string
	Key    string
	TS     string
}

// fetchLongPollServer calls groups.getLongPollServer and returns the
// (server, key, ts) triple for the next a_check loop.
func (v *VK) fetchLongPollServer(ctx context.Context) (*longPollServer, error) {
	resp, err := v.callAPI(ctx, "groups.getLongPollServer", map[string]string{
		"group_id": strconv.FormatInt(v.GroupID, 10),
	})
	if err != nil {
		return nil, fmt.Errorf("vk: getLongPollServer: %w", err)
	}
	// The Bot API response has nested `response` when successful and
	// top-level `error` when not. We split it manually because the
	// `response` field is `any`-typed in our Send code path.
	var envelope struct {
		Response json.RawMessage `json:"response"`
		Error    *struct {
			ErrorCode int    `json:"error_code"`
			ErrorMsg  string `json:"error_msg"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &envelope); err != nil {
		return nil, fmt.Errorf("vk: getLongPollServer parse: %w", err)
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("vk: getLongPollServer api: %d: %s",
			envelope.Error.ErrorCode, envelope.Error.ErrorMsg)
	}
	var out struct {
		Server string `json:"server"`
		Key    string `json:"key"`
		TS     string `json:"ts"`
	}
	if err := json.Unmarshal(envelope.Response, &out); err != nil {
		return nil, fmt.Errorf("vk: getLongPollServer response parse: %w", err)
	}
	if out.Server == "" || out.Key == "" || out.TS == "" {
		return nil, errors.New("vk: getLongPollServer returned empty fields")
	}
	return &longPollServer{
		Server: out.Server,
		Key:    out.Key,
		TS:     out.TS,
	}, nil
}

// callAPI is a small POST helper for the Bot API. Throws on error
// envelope rather than silently returning a body.
func (v *VK) callAPI(ctx context.Context, method string, params map[string]string) ([]byte, error) {
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	form.Set("access_token", v.Token)
	form.Set("v", v.APIVersion)

	endpoint := v.baseURL + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("vk: %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vk: %s send: %w", method, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("vk: %s: %s", method, resp.Status)
	}
	var errEnv struct {
		Error *struct {
			ErrorCode int    `json:"error_code"`
			ErrorMsg  string `json:"error_msg"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errEnv); err == nil && errEnv.Error != nil {
		return nil, fmt.Errorf("vk: %s: %d: %s", method, errEnv.Error.ErrorCode, errEnv.Error.ErrorMsg)
	}
	return body, nil
}

// aCheckResponse is the long-poll envelope. Success:
//
//	{"ts": <new_ts>, "updates": [[type, ...], ...]}
//
// Failure (one of the `failed` codes — server/key/ts went stale):
//
//	{"failed": 1, "ts": <new_ts>}
type aCheckResponse struct {
	TS      string                     `json:"ts"`
	Failed  int                        `json:"failed"`
	Updates [][]any                    `json:"updates"`
	Extra   map[string]json.RawMessage `json:"-"`
}

// pollLoop runs the long-poll loop until ctx is done or Stop is
// called. On errors it sleeps (with jitter) and re-fetches the
// server. The intent is "the loop never exits unless asked" — every
// transient network glitch or vk API hiccup is recoverable.
func (v *VK) pollLoop(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-v.stopCh:
			return
		default:
		}

		server, err := v.fetchLongPollServer(ctx)
		if err != nil {
			if v.OnError != nil {
				v.OnError(fmt.Errorf("getLongPollServer: %w", err))
			}
			if !v.waitOrExit(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		backoff = time.Second // reset on success

		// Inner loop: hold the (server, key, ts) handle until failed.
		for {
			select {
			case <-ctx.Done():
				return
			case <-v.stopCh:
				return
			default:
			}

			updates, newTS, retry, err := v.aCheck(ctx, server)
			if err != nil {
				if v.OnError != nil {
					v.OnError(fmt.Errorf("a_check: %w", err))
				}
				if !v.waitOrExit(ctx, backoff) {
					return
				}
				backoff = nextBackoff(backoff, maxBackoff)
				break // re-fetch server
			}
			backoff = time.Second
			if retry {
				// failed=1 → server/key rotated; the new ts is in
				// the response. Re-fetch the server (and key).
				break
			}
			server.TS = newTS
			for _, u := range updates {
				v.dispatch(ctx, u)
			}
		}
	}
}

// aCheck performs one long-poll request. Returns the updates slice,
// the new ts, a `retry` flag (true when the server says failed=1 and
// we should re-fetch groups.getLongPollServer), and any err worth
// surfacing.
func (v *VK) aCheck(ctx context.Context, server *longPollServer) (updates [][]any, newTS string, retry bool, err error) {
	// The `server` field from groups.getLongPollServer is a
	// "host:port" string. Production: scheme is https://. Tests
	// inject a different scheme to talk to httptest servers.
	endpoint := v.lpScheme + server.Server
	values := url.Values{
		"act":  {"a_check"},
		"key":  {server.Key},
		"ts":   {server.TS},
		"wait": {strconv.Itoa(int(v.PollTimeout.Seconds()))},
		"mode": {"2|8|32"}, // attachments + extended events + random_id
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+values.Encode(), http.NoBody)
	if err != nil {
		return nil, "", false, fmt.Errorf("vk: a_check request: %w", err)
	}

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("vk: a_check send: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, "", false, fmt.Errorf("vk: a_check: %s", resp.Status)
	}

	var ac aCheckResponse
	if err := json.Unmarshal(body, &ac); err != nil {
		return nil, "", false, fmt.Errorf("vk: a_check parse: %w", err)
	}
	if ac.Failed != 0 {
		// failed=1: history is lost or stored ts is too old; re-fetch
		// any other code (2/3/4) is a structural error — bail to
		// outer loop and re-fetch the server.
		return nil, ac.TS, true, nil
	}
	return ac.Updates, ac.TS, false, nil
}

// dispatch routes a single update tuple to the registered callbacks.
// We only handle `message_new` (type 4); everything else is silently
// dropped with the ts acknowledged. The contract is that we MUST
// advance ts for every update we receive, otherwise the server
// redelivers the same batch forever.
func (v *VK) dispatch(ctx context.Context, u []any) {
	if len(u) == 0 {
		return
	}
	raw, ok := u[0].(float64) // JSON numbers decode as float64
	if !ok {
		return
	}
	code := int(raw)
	if code != 4 || v.OnMessage == nil {
		return
	}
	// message_new payload shape (subset):
	//   [4, flags, from_id, timestamp, text, {attachments}, conversation_message_id, peer_id, ...]
	msg := v.parseMessageNew(u)
	if msg == nil {
		return
	}
	_ = v.OnMessage(ctx, *msg)
}

// parseMessageNew extracts the inbox-relevant fields from a
// message_new update. Returns nil when the shape is wrong (we silently
// drop the update — see OnMessage for the failure surface).
func (v *VK) parseMessageNew(u []any) *InboxMessage {
	if len(u) < 6 {
		return nil
	}
	fromID, _ := u[2].(float64)
	text, _ := u[4].(string)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	convID, _ := u[6].(float64)
	peerID, _ := u[7].(float64)
	return &InboxMessage{
		ChatID:    int64(peerID),
		MessageID: int(convID),
		UserID:    int64(fromID),
		Text:      text,
	}
}

// waitOrExit sleeps for d, returning false if the goroutine should
// exit (ctx cancelled or stopCh closed).
func (v *VK) waitOrExit(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-v.stopCh:
		return false
	case <-t.C:
		return true
	}
}

// nextBackoff doubles d up to limit. We don't add jitter — the loop
// is single-attempt and the next server fetch is the actual backoff.
func nextBackoff(d, limit time.Duration) time.Duration {
	if d >= limit {
		return limit
	}
	d *= 2
	if d > limit {
		d = limit
	}
	return d
}

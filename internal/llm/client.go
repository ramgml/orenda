// Package llm provides a minimal OpenAI-compatible chat-completions
// client and the semantic quiz grader built on top of it.
//
// The client speaks the POST {base}/chat/completions wire protocol
// shared by OpenAI, local ollama/vLLM gateways, and most proxy
// providers: a bearer key (optional for local runtimes), a model id,
// and a messages array. Only the fields Orenda needs are modelled —
// no SDK dependency, matching the zero-new-deps convention (the MCP
// server followed the same route).
//
// The grader turns (question, expected answer, student answer) into
// a strict JSON verdict {passed, feedback}. It is deliberately
// provider-agnostic: any model that can follow the JSON instruction
// works, and a malformed reply is an error, never a guessed grade.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal OpenAI-compatible chat-completions client.
type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// NewClient builds a client rooted at baseURL (including the version
// prefix, e.g. "https://api.openai.com/v1"). timeout bounds one
// HTTP round-trip; <= 0 falls back to 30s.
func NewClient(baseURL, apiKey, model string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: timeout},
	}
}

// chatMessage is one entry of the messages array.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest is the subset of the chat-completions body Orenda sends.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

// chatResponse is the subset of the reply Orenda parses. Choices is
// optional in the wire format, so a missing field decodes to an
// empty slice and surfaces as ErrEmptyResponse.
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// ErrEmptyResponse is returned when the API replies without any
// choice content — retrying the same request rarely helps, so it is
// a distinct sentinel.
var ErrEmptyResponse = fmt.Errorf("llm: empty response")

// Complete sends one chat turn and returns the assistant's text.
func (c *Client) Complete(ctx context.Context, system, user string) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature: 0,
	})
	if err != nil {
		return "", fmt.Errorf("llm: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: post chat/completions: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-side close, nothing to do
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: api status %d", resp.StatusCode)
	}
	var parsed chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("llm: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", ErrEmptyResponse
	}
	return parsed.Choices[0].Message.Content, nil
}

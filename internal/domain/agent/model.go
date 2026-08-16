// Package agent holds the Agent domain entity.
//
// An agent is an AI-driven program client identified by an opaque API token
// (see internal/auth/apitoken.go). The token's bcrypt hash is stored in
// api_tokens.hash; the agent row references the token by id (FK) so deleting
// the token cascades to the agent.
package agent

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// Status is the operational status of an agent.
//
// The StatusCalculator (Phase 3.5) flips online → offline after 2 minutes
// without a heartbeat. Disabled is an admin override.
type Status string

const (
	StatusOnline   Status = "online"
	StatusOffline  Status = "offline"
	StatusDisabled Status = "disabled"
)

// Sentinel errors returned by Repository.
var (
	ErrNotFound     = errors.New("agent: not found")
	ErrNameTaken    = errors.New("agent: name already in use")
	ErrInvalidInput = errors.New("agent: invalid input")
)

// Agent is the canonical agent entity.
//
// Type is a free-form, operator-curated set of labels (e.g. "qwen",
// "installer", "claude") — useful for UI grouping and for an agent-side
// `GET /api/v1/agents?type=qwen` filter. There is no fixed enum: the
// catalogue lives in the operator's head, not in code. Validate applies
// a deterministic normalisation (trim, lowercase, dedupe, sort) so the
// same logical set always serialises identically.
type Agent struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Type          []string   `json:"type"`
	Description   string     `json:"description,omitempty"`
	TokenID       string     `json:"token_id"`
	LastSeenAt    *time.Time `json:"last_seen_at,omitempty"`
	Status        Status     `json:"status"`
	MaxConcurrent int        `json:"max_concurrent"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Validate returns an error if the Agent fields are inconsistent and
// normalises the Type label set in place. An empty/nil Type is valid —
// agents may exist without any label.
func (a *Agent) Validate() error {
	if a.Name == "" {
		return ErrInvalidInput
	}
	a.Type = NormalizeLabels(a.Type)
	if a.MaxConcurrent <= 0 {
		a.MaxConcurrent = 3
	}
	if a.Status == "" {
		a.Status = StatusOffline
	}
	return nil
}

// NormalizeLabels trims, lowercases, dedupes and sorts the input slice.
// Empty/whitespace-only labels are dropped. A nil or empty input returns
// an empty (non-nil) slice so JSON encoding always emits "[]" rather
// than "null" — the storage layer's backfill migration guarantees that
// every row in the database carries a JSON array literal.
//
// Exported because tests (and the API handler that builds a request
// payload from arbitrary user input) want to reuse the same canonical
// shape that Validate applies internally.
func NormalizeLabels(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		l := strings.ToLower(strings.TrimSpace(raw))
		if l == "" {
			continue
		}
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// IsAlive returns true when the agent was last seen within ttl.
//
// ttl=0 means "exactly now" — useful for unit tests that want to assert
// the calculator's decision boundary.
func (a *Agent) IsAlive(ttl time.Duration) bool {
	if a.LastSeenAt == nil {
		return false
	}
	return time.Since(*a.LastSeenAt) <= ttl
}

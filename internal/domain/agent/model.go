// Package agent holds the Agent domain entity.
//
// An agent is an AI-driven program client identified by an opaque API token
// (see internal/auth/apitoken.go). The token's bcrypt hash is stored in
// api_tokens.hash; the agent row references the token by id (FK) so deleting
// the token cascades to the agent.
package agent

import (
	"errors"
	"time"
)

// Type identifies the runtime an agent is implemented on (used for UI
// grouping and Phase 10 bot dispatch).
type Type string

const (
	TypeQwen   Type = "qwen"
	TypeClaude Type = "claude"
	TypeCustom Type = "custom"
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
type Agent struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Type          Type       `json:"type"`
	Description   string     `json:"description,omitempty"`
	TokenID       string     `json:"token_id"`
	LastSeenAt    *time.Time `json:"last_seen_at,omitempty"`
	Status        Status     `json:"status"`
	MaxConcurrent int        `json:"max_concurrent"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Validate returns an error if the Agent fields are inconsistent.
func (a *Agent) Validate() error {
	if a.Name == "" {
		return ErrInvalidInput
	}
	if a.Type == "" {
		a.Type = TypeCustom
	}
	if a.MaxConcurrent <= 0 {
		a.MaxConcurrent = 3
	}
	if a.Status == "" {
		a.Status = StatusOffline
	}
	return nil
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

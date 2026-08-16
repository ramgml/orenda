package agent

import (
	"context"
	"time"
)

// Repository persists and retrieves Agents.
type Repository interface {
	Create(ctx context.Context, a *Agent) error
	GetByID(ctx context.Context, id string) (*Agent, error)
	GetByName(ctx context.Context, name string) (*Agent, error)
	GetByTokenID(ctx context.Context, tokenID string) (*Agent, error)
	List(ctx context.Context) ([]*Agent, error)
	Update(ctx context.Context, a *Agent) error
	Delete(ctx context.Context, id string) error

	// TouchLastSeen sets last_seen_at = now for the given id and returns
	// the updated row. Used by Heartbeat.
	TouchLastSeen(ctx context.Context, id string) (*Agent, error)

	// SweepOffline marks every agent with last_seen_at older than ttl as
	// offline. Returns the number of rows updated.
	SweepOffline(ctx context.Context, ttl time.Duration) (int64, error)
	// ListStaleOnlineAgents returns the agents that the next sweep
	// will flip to offline. Phase Wave 4 PR 2: notifier fans
	// `agent.offline` events per-agent from the result of this
	// call before the SweepOffline UPDATE. Best-effort, no
	// round-trip guarantee.
	ListStaleOnlineAgents(ctx context.Context, ttl time.Duration) ([]*Agent, error)
}

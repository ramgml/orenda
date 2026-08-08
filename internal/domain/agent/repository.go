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
	// offline. Returns the number of rows updated; Phase 3.5 calls this
	// every 30s from a background worker.
	SweepOffline(ctx context.Context, ttl time.Duration) (int64, error)
}

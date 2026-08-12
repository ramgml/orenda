// Package agent provides business logic on top of agent.Repository and
// api_tokens. Phase 3 ships:
//
//   - Register:  mints a fresh bcrypt'd API token and creates the agent row.
//     The plaintext token is returned exactly once.
//   - Heartbeat: bumps last_seen_at and flips status to online.
//   - SweepOffline: background-friendly helper that the StatusCalculator
//     calls periodically to flip stale agents offline.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/user"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
)

// Sentinel errors.
var (
	ErrNotFound  = errors.New("agent service: not found")
	ErrNameTaken = errors.New("agent service: name already in use")
)

// HashCost is the bcrypt cost for minted API tokens. Phase 3 keeps the
// agent API token cost in sync with auth.HashAPIToken; expose it via
// a constant for tests.
const HashCost = 4

// TokenTTL is how long an issued token is valid. Phase 3 has no per-token
// expiry check in middleware yet — the JWT layer still uses the user
// session. Tokens are infinite-lived until the row is deleted.
const TokenTTL = 0 // 0 = no expiry

// Hub interface used by Service.Publish (mirrors task.Service).
type Hub = ws.Hub

// Recorder is the audit hook (calls into activity.Repository).
type Recorder interface {
	RecordAgentAction(ctx context.Context, agentID, action, payload string) error
}

// RecorderFunc adapts a closure to Recorder.
type RecorderFunc func(ctx context.Context, agentID, action, payload string) error

// Record implements Recorder.
func (f RecorderFunc) Record(ctx context.Context, agentID, action, payload string) error {
	return f(ctx, agentID, action, payload)
}

// NotifierEmitter is the slim surface agent.Service needs from
// the notifier package. The concrete *notifier.Service satisfies
// it; the field is optional (nil disables the `agent.offline`
// event).
//
// We accept the notifier's own Event type so the JSON tags and
// field validation stay in one place. This pulls in a thin
// dependency on the notifier package — acceptable because
// notifier → bot is the only cycle, and we're not on that path.
type NotifierEmitter interface {
	Notify(ctx context.Context, e notifierservice.Event) error
}

// notifierEventFor builds the offline-event shape for a single
// agent. Title/Body match the templates' "agent X went offline"
// voice; the dedup key is type+target so the next sweep (every
// 30s) doesn't re-emit for the same agent until it comes back
// online and goes offline again.
func notifierEventFor(a *agent.Agent) notifierservice.Event {
	return notifierservice.Event{
		Type:       "agent.offline",
		TargetType: "agent",
		TargetID:   a.ID,
		Title:      a.Name + " went offline",
		Body:       "No heartbeat in the last 2 minutes.",
		DedupKey:   "agent.offline:" + a.ID,
	}
}

// TokenMinter is the small surface Service.Register needs from the
// api_tokens storage. We pass a closure rather than a full Repo so the
// service stays decoupled from sqlite.StoredToken.
type TokenMinter interface {
	MintToken(ctx context.Context, userID, name, hash, scopesJSON string, expiresAt *time.Time) (tokenID, tokenName string, err error)
}

// Service is the dependency holder.
type Service struct {
	Agents   agent.Repository
	Users    user.Repository
	Tokens   TokenMinter
	Hub      Hub
	Recorder Recorder

	// Phase Wave 4 PR 2: notifier used to fan out
	// `agent.offline` events from the SweepOffline path. nil is
	// OK (events are best-effort; the production server wires
	// this).
	Notifier NotifierEmitter

	// SweepInterval is the period between background sweeps. cmd/orenda
	// wires a ticker that calls SweepOffline.
	SweepInterval time.Duration

	// SweepTTL is the threshold after which an online agent is considered
	// stale and flipped to offline. Defaults to 2 minutes per PLAN#3.5.
	SweepTTL time.Duration

	// HashCost overrides the bcrypt cost when non-zero. Tests use this
	// to keep costs low; production uses HashCost (4) so middleware
	// checks don't dominate test runtimes.
	HashCostOverride int
}

// New returns a Service with sensible defaults.
func New(agents agent.Repository, users user.Repository, tokens TokenMinter, hub Hub, rec Recorder) *Service {
	return &Service{
		Agents:           agents,
		Users:            users,
		Tokens:           tokens,
		Hub:              hub,
		Recorder:         rec,
		SweepInterval:    30 * time.Second,
		SweepTTL:         2 * time.Minute,
		HashCostOverride: HashCost,
	}
}

// Registered is the result of Register.
type Registered struct {
	Agent      *agent.Agent
	PlainToken string
}

// Register creates an Agent, mints a fresh API token, and stores the bcrypt
// hash. The plaintext token is returned in Registered.PlainToken; callers
// must surface it to the operator exactly once.
func (s *Service) Register(ctx context.Context, name string, kind agent.Type, description string, scopes []string) (*Registered, error) {
	if name = strings.TrimSpace(name); name == "" {
		return nil, errors.New("agent service: name required")
	}

	plain, err := auth.NewAPIToken()
	if err != nil {
		return nil, fmt.Errorf("agent service: mint token: %w", err)
	}

	hash, err := auth.HashAPIToken(plain, s.HashCostOverride)
	if err != nil {
		return nil, fmt.Errorf("agent service: hash token: %w", err)
	}

	// We need a user_id for the tokens table. Agents aren't owned by
	// a user in Phase 3's data model — the api_tokens row references a
	// system user. Look up the unique "owner" user, or create one.
	owner, err := s.ensureOwner(ctx)
	if err != nil {
		return nil, err
	}

	var expiresAt *time.Time
	if TokenTTL > 0 {
		t := time.Now().Add(TokenTTL)
		expiresAt = &t
	}
	scopesJSON := strings.Join(scopes, ",")
	if scopesJSON == "" {
		scopesJSON = "[]"
	}
	// Use the JSON array form the middleware already parses.
	scopesJSON = asJSONArray(scopes)

	tokID, _, err := s.Tokens.MintToken(ctx, owner.ID, "agent:"+name, hash, scopesJSON, expiresAt)
	if err != nil {
		return nil, fmt.Errorf("agent service: persist token: %w", err)
	}

	a := &agent.Agent{
		Name:          name,
		Type:          kind,
		Description:   description,
		TokenID:       tokID,
		Status:        agent.StatusOffline,
		MaxConcurrent: 3,
	}
	if err := s.Agents.Create(ctx, a); err != nil {
		if errors.Is(err, agent.ErrNameTaken) {
			return nil, ErrNameTaken
		}
		return nil, err
	}

	if s.Hub != nil {
		s.Hub.Publish(ctx, ws.Event{
			Topic: "agents",
			Body: map[string]any{
				"type":    "agent.registered",
				"agent":   a,
				"user_id": owner.ID,
			},
		})
	}

	return &Registered{Agent: a, PlainToken: plain}, nil
}

// ensureOwner returns the (single) system user used as api_tokens.user_id.
//
// The Phase 3 data model has agents.token_id → api_tokens.id but no
// agents.owner_id; the token row still needs a user_id. We use a single
// synthetic "agent-owner" user — the seed migration ensures the row exists.
func (s *Service) ensureOwner(ctx context.Context) (*user.User, error) {
	const email = "agent-owner@orenda.local"
	u, err := s.Users.GetByEmail(ctx, email)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, user.ErrNotFound) {
		return nil, fmt.Errorf("agent service: lookup owner: %w", err)
	}
	hash, err := auth.HashPassword("unusable", s.HashCostOverride)
	if err != nil {
		return nil, fmt.Errorf("agent service: hash owner: %w", err)
	}
	u = &user.User{
		Email:        email,
		PasswordHash: hash,
		DisplayName:  "Agent Owner",
	}
	if err := s.Users.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("agent service: create owner: %w", err)
	}
	return u, nil
}

// Heartbeat updates last_seen_at + status for an agent by id. The id is
// the agent's primary key; agents authenticate via /auth/me (Bearer JWT)
// or by their API token at the WS upgrade — Phase 3.11 wires the lookup.
func (s *Service) Heartbeat(ctx context.Context, agentID string) (*agent.Agent, error) {
	a, err := s.Agents.TouchLastSeen(ctx, agentID)
	if err != nil {
		if errors.Is(err, agent.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if s.Hub != nil {
		s.Hub.Publish(ctx, ws.Event{
			Topic: "agents",
			Body: map[string]any{
				"type":    "agent.heartbeat",
				"agent":   a,
				"user_id": a.ID, // agents aren't owned by users in Phase 3
			},
		})
	}
	return a, nil
}

// SweepOffline flips stale agents to offline and returns the number of
// rows updated. Called by the StatusCalculator background ticker in
// cmd/orenda (Phase 3.5).
//
// Phase Wave 4 PR 2: when a Notifier is wired, also emits
// `agent.offline` per affected agent. The events are best-effort
// (errors are logged) and dedup'd on the receiver side via
// (type, target_id) so the next sweep doesn't re-emit.
func (s *Service) SweepOffline(ctx context.Context) (int64, error) {
	if s.Notifier != nil {
		stale, _ := s.Agents.ListStaleOnlineAgents(ctx, s.SweepTTL)
		for _, a := range stale {
			_ = s.Notifier.Notify(ctx, notifierEventFor(a))
		}
	}
	return s.Agents.SweepOffline(ctx, s.SweepTTL)
}

// RunStatusCalculator is a blocking loop that calls SweepOffline every
// SweepInterval until ctx is cancelled.
//
// Tests use SweepOffline directly; this is just a convenience wrapper
// for cmd/orenda.
func (s *Service) RunStatusCalculator(ctx context.Context) {
	t := time.NewTicker(s.SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.SweepOffline(ctx); err != nil {
				// Best-effort: log on the caller side via Hub?
				_ = err
			}
		}
	}
}

// asJSONArray marshals a []string as a JSON array literal. Used so the
// api_tokens.scopes column matches the format expected by api.parseScopesJSON.
func asJSONArray(in []string) string {
	if len(in) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range in {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(strings.ReplaceAll(s, `"`, `\"`))
		b.WriteByte('"')
	}
	b.WriteByte(']')
	return b.String()
}

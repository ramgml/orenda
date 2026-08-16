// Package api — auth middleware (cookie/JWT, Bearer API-token, scope check).
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/user"
)

// ctxKey is an unexported type for context keys to prevent collisions.
type ctxKey int

const (
	ctxIdentityKey ctxKey = iota
)

// Identity is what auth middleware attaches to the request context.
//
// Exactly one of UserID or AgentID is set — UserID for cookie/JWT sessions,
// AgentID for Bearer API tokens. Scopes are parsed from the JWT or the
// api_tokens row and exposed to RequireScope().
type Identity struct {
	UserID  string
	AgentID string
	Email   string
	Scopes  []string
}

// HasScope reports whether the identity carries the requested scope.
func (i *Identity) HasScope(s string) bool {
	for _, x := range i.Scopes {
		if x == s {
			return true
		}
	}
	return false
}

// IdentityFrom returns the Identity attached to ctx (if any).
func IdentityFrom(ctx context.Context) (*Identity, bool) {
	id, ok := ctx.Value(ctxIdentityKey).(*Identity)
	return id, ok
}

// WithIdentity returns a new context carrying id.
//
// Exported so handlers can attach a pre-built identity in tests (and so future
// Phase 6 long-poll endpoints can propagate identity through internal
// hand-offs without re-parsing the JWT).
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, ctxIdentityKey, id)
}

// withIdentity is the internal alias used by the middleware in this package.
func withIdentity(ctx context.Context, id *Identity) context.Context {
	return WithIdentity(ctx, id)
}

// AuthConfig bundles the dependencies required by auth middleware.
type AuthConfig struct {
	Signer     *auth.Signer
	Users      user.Repository
	Tokens     APITokenLookup
	Agents     agent.Repository
	CookieName string
}

// APITokenLookup is the small surface the auth middleware needs from the
// api_tokens storage. Defining it as an interface here avoids an import cycle
// with internal/storage/sqlite.
type APITokenLookup interface {
	ListAllHashes(ctx context.Context) (map[string]auth.TokenRow, error)
	TouchLastUsed(ctx context.Context, id string) error
}

// parseScopesJSON decodes the JSON array stored in api_tokens.scopes.
func parseScopesJSON(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// RequireUser middleware accepts either:
//
//   - Cookie "orenda_session" containing a valid JWT, OR
//   - Authorization: Bearer <jwt>
//
// On success, the user is loaded from the repository and attached to the
// request context. On failure, 401 is returned with no body.
//
// Endpoints under /api/v1/me and /api/v1/auth/logout use this; so does
// the human UI in general.
func RequireUser(cfg AuthConfig) func(http.Handler) http.Handler {
	cookieName := cfg.CookieName
	if cookieName == "" {
		cookieName = "orenda_session"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := extractUserToken(r, cookieName)
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			claims, err := cfg.Signer.Verify(raw)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			u, err := cfg.Users.GetByID(r.Context(), claims.Subject)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			id := &Identity{
				UserID: u.ID,
				Email:  u.Email,
				Scopes: scopesForRole(u.Role),
			}
			next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), id)))
		})
	}
}

// extractUserToken pulls the JWT from cookie or Authorization header.
func extractUserToken(r *http.Request, cookieName string) (string, bool) {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer "), true
	}
	return "", false
}

// RequireAgent middleware accepts Authorization: Bearer <api-token> and
// resolves it through the api_tokens repository.
//
// On success the identity's AgentID is set to the agent row that owns
// the token (looked up via token_id). Scopes come from the token's stored
// scopes_json. On failure 401 is returned.
func RequireAgent(cfg AuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			plain := auth.NormalizeAPIToken(strings.TrimPrefix(h, "Bearer "))
			if plain == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			tok, err := verifyAPIToken(r.Context(), cfg.Tokens, plain)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			// Look up the agent row to populate AgentID (the token's UserID
			// is the synthetic "agent-owner" user; the real agent id lives
			// in agents.token_id).
			id := &Identity{
				AgentID: "",
				Scopes:  parseScopesJSON(tok.ScopesJSON),
			}
			if cfg.Agents != nil {
				if a, err := cfg.Agents.GetByTokenID(r.Context(), tok.ID); err == nil && a != nil {
					id.AgentID = a.ID
				} else {
					_ = err
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
			} else {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			// Fire-and-forget last_used_at update; failure here doesn't
			// break the request.
			go func(tid string) {
				_ = cfg.Tokens.TouchLastUsed(context.Background(), tid)
			}(tok.ID)
			next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), id)))
		})
	}
}

// verifyAPIToken is a small wrapper around the repo lookup that retries on
// transient errors.
func verifyAPIToken(ctx context.Context, repo APITokenLookup, plain string) (*auth.TokenRow, error) {
	hashes, err := repo.ListAllHashes(ctx)
	if err != nil {
		return nil, err
	}
	for hash, t := range hashes {
		if err := auth.VerifyAPIToken(hash, plain); err == nil {
			return &t, nil
		}
	}
	return nil, errAPITokenNotFound
}

// errAPITokenNotFound is private; surface as 401 in the middleware.
var errAPITokenNotFound = authError("apitoken: not found")

type authError string

func (e authError) Error() string { return string(e) }

// RequireScope returns a middleware that rejects requests whose identity
// lacks the requested scope. Place AFTER RequireUser or RequireAgent.
//
// In Phase 1 user tokens get their scopes from the role (RoleOwner -> all
// scopes); agent tokens carry an explicit list from api_tokens.scopes.
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := IdentityFrom(r.Context())
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if !id.HasScope(scope) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// scopesForRole returns the implicit scope set for a user role.
//
// Phase 1: owner has every scope; other roles (none defined yet) get none.
// Phase 2+ will replace this with explicit per-role policies.
func scopesForRole(r user.Role) []string {
	switch r {
	case user.RoleOwner:
		return []string{
			"tasks:read", "tasks:write",
			"projects:read", "projects:write",
			"agents:read", "agents:write",
			"settings:read", "settings:write",
		}
	default:
		return nil
	}
}

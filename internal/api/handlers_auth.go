// Package api — authentication handlers (login, logout, me).
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ramgml/orenda/internal/auth"
)

// loginRequest is the JSON body of POST /api/v1/auth/login.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// loginResponse is the payload returned on successful login.
type loginResponse struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Token       string `json:"token"`
}

// loginHandler authenticates by email + password and issues a session cookie
// + JSON token.
func loginHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if req.Email == "" || req.Password == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_credentials"})
			return
		}

		u, err := deps.Users.GetByEmail(r.Context(), req.Email)
		if err != nil {
			// Don't leak whether the email exists; same response either way.
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
			return
		}
		if err := auth.VerifyPassword(u.PasswordHash, req.Password); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_credentials"})
			return
		}

		tok, err := deps.Signer.Issue(u.ID, u.DisplayName, string(u.Role))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "sign_failed"})
			return
		}

		// Set httpOnly cookie for the SPA. SameSite=Lax is the OWASP
		// recommendation for cookie-based auth in 2024. The `Secure`
		// flag is driven by config (auth.cookie_secure) — leave it
		// false on loopback dev installs and flip it on when serving
		// over HTTPS. Phase 28.4 retired the Phase 1 hardcoded false.
		http.SetCookie(w, &http.Cookie{
			Name:     deps.CookieName,
			Value:    tok,
			Path:     "/",
			HttpOnly: true,
			Secure:   deps.CookieSecure,
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Now().Add(deps.JWTTTL),
		})

		writeJSON(w, http.StatusOK, loginResponse{
			UserID:      u.ID,
			Email:       u.Email,
			DisplayName: u.DisplayName,
			Role:        string(u.Role),
			Token:       tok,
		})
	}
}

// logoutHandler clears the session cookie.
//
// Stateless servers can't actually invalidate a JWT, so logout here means
// "drop the cookie". Phase 6 (notifications) will introduce a token
// blacklist for server-side invalidation if needed.
//
// Phase 28.4: also propagate `Secure` from config. If we issued the cookie
// with Secure=true, the matching MaxAge=-1 must carry Secure=true too —
// otherwise some browsers scope the deletion to the non-secure cookie set
// and the secure one survives the logout.
func logoutHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     deps.CookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   deps.CookieSecure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
		writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
	}
}

// meHandler returns the authenticated user's profile.
func meHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no_identity"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"user_id": id.UserID,
			"email":   id.Email,
			"scopes":  id.Scopes,
		})
	}
}

// LoginHandlerForTest is the test seam that lets callers mount
// just the login handler against an arbitrary Dependencies. The
// router wires loginHandler(deps) internally; tests in
// handlers_auth_test.go use this to assert cookie attributes
// (Secure, Expires, SameSite, HttpOnly) without standing up the
// whole API surface.
//
// Phase 28.4 introduced this so the cookie-security regression
// suite can pin Phase 1's hardcoded Secure:false replacement.
func LoginHandlerForTest(deps Dependencies) http.Handler {
	return loginHandler(deps)
}

// LogoutHandlerForTest mirrors LoginHandlerForTest for the
// logout endpoint. The MaxAge=-1 cookie must carry the same
// Secure value as the login one — same regression risk, same
// fix, same test.
func LogoutHandlerForTest(deps Dependencies) http.Handler {
	return logoutHandler(deps)
}

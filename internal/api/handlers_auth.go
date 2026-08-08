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
		// recommendation for cookie-based auth in 2024.
		http.SetCookie(w, &http.Cookie{
			Name:     deps.CookieName,
			Value:    tok,
			Path:     "/",
			HttpOnly: true,
			Secure:   false, // Phase 1: loopback only; flag toggles in Phase 9
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Now().Add(24 * time.Hour),
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
func logoutHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     deps.CookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
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

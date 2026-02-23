package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/ypk/downloadonce/internal/auth"
	"github.com/ypk/downloadonce/internal/db"
)

func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var accountID string

		// Check API key first (Authorization: Bearer do_...)
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer do_") {
			apiKey := strings.TrimPrefix(authHeader, "Bearer ")
			id, ok := h.validateAPIKey(apiKey)
			if !ok {
				http.Error(w, "Invalid API key", http.StatusUnauthorized)
				return
			}
			accountID = id
		} else {
			// Fall back to session cookie
			sessionID, ok := auth.GetSessionID(r, h.Cfg.SessionSecret)
			if !ok {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			session, err := db.GetSession(h.DB, sessionID)
			if err != nil || session == nil || session.ExpiresAt.Before(time.Now()) {
				auth.ClearSessionCookie(w)
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			accountID = session.AccountID
		}

		// Load account to get role and enabled status
		account, err := db.GetAccountByID(h.DB, accountID)
		if err != nil || account == nil {
			auth.ClearSessionCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if !account.Enabled {
			auth.ClearSessionCookie(w)
			db.DeleteSessionsByAccount(h.DB, accountID)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		ctx := auth.ContextWithAccountAndRole(r.Context(), accountID, account.Role, account.Name)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handler) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.IsAdmin(r.Context()) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) validateAPIKey(key string) (string, bool) {
	// Key format: do_<64 hex chars>
	// Prefix for DB lookup: first 8 chars after "do_"
	withoutPrefix := strings.TrimPrefix(key, "do_")
	if len(withoutPrefix) < 8 {
		return "", false
	}
	prefix := withoutPrefix[:8]

	apiKey, err := db.GetAPIKeyByPrefix(h.DB, prefix)
	if err != nil || apiKey == nil {
		return "", false
	}

	if !auth.CheckPassword(apiKey.KeyHash, key) {
		return "", false
	}

	// Update last used timestamp
	go db.TouchAPIKeyUsed(h.DB, apiKey.ID)

	return apiKey.AccountID, true
}

func (h *Handler) RequireSetup(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/setup" || r.URL.Path == "/static/style.css" {
			next.ServeHTTP(w, r)
			return
		}
		exists, _ := db.AccountExists(h.DB)
		if !exists {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

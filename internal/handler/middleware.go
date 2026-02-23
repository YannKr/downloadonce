package handler

import (
	"net/http"
	"time"

	"github.com/ypk/downloadonce/internal/auth"
	"github.com/ypk/downloadonce/internal/db"
)

func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		ctx := auth.ContextWithAccount(r.Context(), session.AccountID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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

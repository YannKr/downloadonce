package handler

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/YannKr/downloadonce/internal/auth"
	"github.com/YannKr/downloadonce/internal/db"
)

// RealIP rewrites r.RemoteAddr to the client IP address. X-Forwarded-For and
// X-Real-IP are only read when the connection itself came from an address in
// TRUSTED_PROXIES; otherwise any client could set its own IP and defeat the
// rate limiter and the audit log. Everything downstream reads r.RemoteAddr.
func (h *Handler) RealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = clientIP(r, h.Cfg.TrustedProxies)
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request, trusted []netip.Prefix) string {
	remote := hostOnly(r.RemoteAddr)
	if len(trusted) == 0 || !trustedAddr(remote, trusted) {
		return remote
	}

	// Walk X-Forwarded-For right to left and take the first hop that is not
	// one of our own proxies. Entries to the left of it are attacker-supplied.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			addr, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
			if err != nil {
				continue
			}
			if !prefixesContain(trusted, addr) {
				return addr.String()
			}
		}
	}
	if addr, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
		return addr.String()
	}
	return remote
}

func trustedAddr(host string, trusted []netip.Prefix) bool {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return prefixesContain(trusted, addr)
}

func prefixesContain(trusted []netip.Prefix, addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// hostOnly strips the port from a "host:port" address, if there is one.
func hostOnly(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// SecurityHeaders sets the response headers that cost nothing and close off
// clickjacking, MIME sniffing and referrer leakage of download links. HSTS is
// only sent when the site is configured for HTTPS.
func (h *Handler) SecurityHeaders(next http.Handler) http.Handler {
	https := strings.HasPrefix(h.Cfg.BaseURL, "https")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if https {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

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

// requireAPIAuth validates Bearer API keys and returns JSON errors (not redirects).
func (h *Handler) requireAPIAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer do_") {
			renderJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or missing API key")
			return
		}
		apiKey := strings.TrimPrefix(authHeader, "Bearer ")
		accountID, ok := h.validateAPIKey(apiKey)
		if !ok {
			renderJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or missing API key")
			return
		}
		account, err := db.GetAccountByID(h.DB, accountID)
		if err != nil || account == nil || !account.Enabled {
			renderJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "account is disabled or not found")
			return
		}
		ctx := auth.ContextWithAccountAndRole(r.Context(), accountID, account.Role, account.Name)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// apiRateLimit returns a middleware that rate-limits by IP and sets X-RateLimit-* headers.
func (h *Handler) apiRateLimit(rl *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limiter := rl.Get(r.RemoteAddr)
			tokens := limiter.Tokens()
			burst := rl.Burst()

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(burst))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(int(tokens)))

			if !limiter.Allow() {
				w.Header().Set("Retry-After", "1")
				renderJSONError(w, http.StatusTooManyRequests, "RATE_LIMITED", "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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

package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

const (
	CookieName    = "downloadonce_session"
	SessionMaxAge = 7 * 24 * time.Hour
)

type contextKey string

const AccountIDKey contextKey = "account_id"
const RoleKey contextKey = "role"
const NameKey contextKey = "name"

func SetSessionCookie(w http.ResponseWriter, sessionID, secret string) {
	sig := sign(sessionID, secret)
	value := sessionID + "." + sig
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(SessionMaxAge.Seconds()),
	})
}

func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func GetSessionID(r *http.Request, secret string) (string, bool) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(cookie.Value, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	sessionID, sig := parts[0], parts[1]
	expected := sign(sessionID, secret)
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return "", false
	}
	return sessionID, true
}

func AccountFromContext(ctx context.Context) string {
	v, _ := ctx.Value(AccountIDKey).(string)
	return v
}

func RoleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(RoleKey).(string)
	return v
}

func NameFromContext(ctx context.Context) string {
	v, _ := ctx.Value(NameKey).(string)
	return v
}

func IsAdmin(ctx context.Context) bool {
	return RoleFromContext(ctx) == "admin"
}

func ContextWithAccount(ctx context.Context, accountID string) context.Context {
	return context.WithValue(ctx, AccountIDKey, accountID)
}

func ContextWithAccountAndRole(ctx context.Context, accountID, role, name string) context.Context {
	ctx = context.WithValue(ctx, AccountIDKey, accountID)
	ctx = context.WithValue(ctx, RoleKey, role)
	ctx = context.WithValue(ctx, NameKey, name)
	return ctx
}

func sign(data, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

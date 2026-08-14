package handler

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/YannKr/downloadonce/internal/config"
)

func prefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatalf("parse %q: %v", c, err)
		}
		out = append(out, p.Masked())
	}
	return out
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		xri        string
		trusted    []string
		want       string
	}{
		{
			name:       "no trusted proxies, headers are ignored",
			remoteAddr: "203.0.113.9:5555",
			xff:        "1.2.3.4",
			xri:        "5.6.7.8",
			want:       "203.0.113.9",
		},
		{
			name:       "untrusted peer cannot claim another IP",
			remoteAddr: "203.0.113.9:5555",
			xff:        "1.2.3.4",
			trusted:    []string{"127.0.0.1/32"},
			want:       "203.0.113.9",
		},
		{
			name:       "trusted proxy supplies the client IP",
			remoteAddr: "127.0.0.1:5555",
			xff:        "198.51.100.7",
			trusted:    []string{"127.0.0.1/32"},
			want:       "198.51.100.7",
		},
		{
			name:       "left-hand forged hops are skipped",
			remoteAddr: "127.0.0.1:5555",
			xff:        "1.2.3.4, 198.51.100.7",
			trusted:    []string{"127.0.0.1/32"},
			want:       "198.51.100.7",
		},
		{
			name:       "our own proxies are skipped from the right",
			remoteAddr: "10.0.0.1:5555",
			xff:        "198.51.100.7, 10.0.0.2",
			trusted:    []string{"10.0.0.0/8"},
			want:       "198.51.100.7",
		},
		{
			name:       "X-Real-IP is used when there is no X-Forwarded-For",
			remoteAddr: "127.0.0.1:5555",
			xri:        "198.51.100.7",
			trusted:    []string{"127.0.0.1/32"},
			want:       "198.51.100.7",
		},
		{
			name:       "garbage headers fall back to the peer address",
			remoteAddr: "127.0.0.1:5555",
			xff:        "not-an-ip",
			trusted:    []string{"127.0.0.1/32"},
			want:       "127.0.0.1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xri != "" {
				r.Header.Set("X-Real-IP", tc.xri)
			}
			got := clientIP(r, prefixes(t, tc.trusted...))
			if got != tc.want {
				t.Fatalf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRealIPMiddlewareRewritesRemoteAddr(t *testing.T) {
	h := &Handler{Cfg: &config.Config{TrustedProxies: prefixes(t, "127.0.0.1/32")}}

	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	})

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	h.RealIP(next).ServeHTTP(httptest.NewRecorder(), r)
	if seen != "198.51.100.7" {
		t.Fatalf("RemoteAddr = %q, want 198.51.100.7", seen)
	}

	// realIP reads the rewritten RemoteAddr and must not look at the header
	// a second time.
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "203.0.113.9:5555"
	r2.Header.Set("X-Forwarded-For", "1.2.3.4")
	h.RealIP(next).ServeHTTP(httptest.NewRecorder(), r2)
	if seen != "203.0.113.9" {
		t.Fatalf("RemoteAddr = %q, want 203.0.113.9", seen)
	}
	if got := realIP(r2); got != "203.0.113.9" {
		t.Fatalf("realIP = %q, want 203.0.113.9", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := &Handler{Cfg: &config.Config{BaseURL: "https://example.test"}}
	rec := httptest.NewRecorder()
	h.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for header, want := range map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "no-referrer",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	// HSTS must not be sent on a plain-http deployment.
	plain := &Handler{Cfg: &config.Config{BaseURL: "http://localhost:8080"}}
	rec2 := httptest.NewRecorder()
	plain.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).
		ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec2.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS on http deployment = %q, want empty", got)
	}
}

func TestRateLimiterKeysOnRemoteAddr(t *testing.T) {
	rl := NewRateLimiter(0.01, 1)
	defer rl.Stop()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mw := rl.Middleware(next)

	req := func(xri string) int {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "203.0.113.9:5555"
		if xri != "" {
			r.Header.Set("X-Real-Ip", xri)
		}
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, r)
		return rec.Code
	}

	if code := req(""); code != http.StatusNoContent {
		t.Fatalf("first request = %d, want 204", code)
	}
	// A fresh X-Real-Ip on each request used to buy a fresh bucket.
	if code := req("1.1.1.1"); code != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", code)
	}
	if code := req("2.2.2.2"); code != http.StatusTooManyRequests {
		t.Fatalf("third request = %d, want 429", code)
	}
}

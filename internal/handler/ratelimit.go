package handler

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter tracks per-IP rate limits using token buckets.
type RateLimiter struct {
	visitors sync.Map
	rate     rate.Limit
	burst    int
	done     chan struct{}
}

// NewRateLimiter creates a rate limiter that allows r requests per second with
// the given burst size. It starts a background goroutine that evicts stale
// entries every 10 minutes.
func NewRateLimiter(r rate.Limit, burst int) *RateLimiter {
	rl := &RateLimiter{
		rate:  r,
		burst: burst,
		done:  make(chan struct{}),
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter {
	v, ok := rl.visitors.Load(ip)
	if ok {
		vis := v.(*visitor)
		vis.lastSeen = time.Now()
		return vis.limiter
	}
	limiter := rate.NewLimiter(rl.rate, rl.burst)
	rl.visitors.Store(ip, &visitor{limiter: limiter, lastSeen: time.Now()})
	return limiter
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.visitors.Range(func(key, value any) bool {
				v := value.(*visitor)
				if time.Since(v.lastSeen) > 10*time.Minute {
					rl.visitors.Delete(key)
				}
				return true
			})
		case <-rl.done:
			return
		}
	}
}

// Stop terminates the background cleanup goroutine.
func (rl *RateLimiter) Stop() {
	close(rl.done)
}

// Rate returns the rate limit (tokens per second).
func (rl *RateLimiter) Rate() rate.Limit {
	return rl.rate
}

// Burst returns the maximum burst size.
func (rl *RateLimiter) Burst() int {
	return rl.burst
}

// Get returns the rate.Limiter for the given IP, creating one if needed.
func (rl *RateLimiter) Get(ip string) *rate.Limiter {
	return rl.getLimiter(ip)
}

// Middleware returns an HTTP middleware that rate-limits by client IP.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if fwd := r.Header.Get("X-Real-Ip"); fwd != "" {
			ip = fwd
		}
		if !rl.getLimiter(ip).Allow() {
			http.Error(w, "Too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

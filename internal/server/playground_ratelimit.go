package server

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/agentserver/agentserver/internal/auth"
)

type rateLimitBucket struct {
	limiter *rate.Limiter
	lastUse time.Time
}

type playgroundRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateLimitBucket
	rps     rate.Limit
	burst   int
}

func newPlaygroundRateLimiter(rps rate.Limit, burst int) *playgroundRateLimiter {
	rl := &playgroundRateLimiter{
		buckets: make(map[string]*rateLimitBucket),
		rps:     rps,
		burst:   burst,
	}
	go rl.evictLoop()
	return rl
}

func (rl *playgroundRateLimiter) allow(userID string) bool {
	if userID == "" {
		userID = "_anonymous"
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, ok := rl.buckets[userID]
	if !ok {
		b = &rateLimitBucket{limiter: rate.NewLimiter(rl.rps, rl.burst)}
		rl.buckets[userID] = b
	}
	b.lastUse = time.Now()
	return b.limiter.Allow()
}

func (rl *playgroundRateLimiter) evictLoop() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for uid, b := range rl.buckets {
			if b.lastUse.Before(cutoff) {
				delete(rl.buckets, uid)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *playgroundRateLimiter) middleware(retryAfterSec int, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := auth.UserIDFromContext(r.Context())
		if !rl.allow(userID) {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSec))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

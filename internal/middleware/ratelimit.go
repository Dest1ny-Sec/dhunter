// Package middleware contains Gin middlewares shared by the API.
package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// LoginLimiter is a small in-memory sliding-window rate limiter for the
// admin login endpoint. It exists to slow online brute-force of the admin
// password: Dhunter has a single static credential pair, so without a limit
// an exposed port (or a local attacker) can spray passwords indefinitely.
//
// Local-first and single-process, so an in-memory map is the right trade-off:
// no external store, resets on restart, and each IP is capped independently.
type LoginLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	max     int
	hits    map[string][]time.Time // client IP -> recent attempt times
	lastGC  time.Time
	gcEvery time.Duration
}

// NewLoginLimiter builds a limiter allowing `max` attempts per `window`
// per client IP. max <= 0 disables the limit.
func NewLoginLimiter(max int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{
		window:  window,
		max:     max,
		hits:    make(map[string][]time.Time),
		lastGC:  time.Now(),
		gcEvery: window,
	}
}

// Middleware returns a Gin handler that rejects requests past the limit
// with 429 + Retry-After. Only rate-limits the exact path it is mounted on
// (callers mount it only on POST /api/auth/login).
func (l *LoginLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if l == nil || l.max <= 0 {
			c.Next()
			return
		}
		ip := c.ClientIP()
		now := time.Now()

		l.mu.Lock()
		// Opportunistic GC: drop expired entries so a long-lived server
		// doesn't accumulate one bucket per attacker IP forever.
		if now.Sub(l.lastGC) >= l.gcEvery {
			for k, times := range l.hits {
				fresh := times[:0]
				for _, t := range times {
					if now.Sub(t) < l.window {
						fresh = append(fresh, t)
					}
				}
				if len(fresh) == 0 {
					delete(l.hits, k)
				} else {
					l.hits[k] = fresh
				}
			}
			l.lastGC = now
		}

		recent := l.hits[ip][:0]
		for _, t := range l.hits[ip] {
			if now.Sub(t) < l.window {
				recent = append(recent, t)
			}
		}
		if len(recent) >= l.max {
			l.hits[ip] = recent
			retryAfter := int(l.window.Seconds())
			l.mu.Unlock()
			c.Header("Retry-After", itoa(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "too many login attempts, slow down",
				"hint":  "rate limited per IP; wait and retry",
			})
			return
		}
		l.hits[ip] = append(recent, now)
		l.mu.Unlock()
		c.Next()
	}
}

// itoa is a tiny int->string helper to avoid strconv for one value.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

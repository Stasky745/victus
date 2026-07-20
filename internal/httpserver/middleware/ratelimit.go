package middleware

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimit returns middleware limiting each client to maxRequests within a
// fixed window — used on /login and /auth/callback to blunt credential/
// OIDC-callback abuse (repeated login attempts, authorization-code
// guessing) per the project's security checklist. In-memory and
// per-process: correct for Victus's single-instance deployment model, no
// shared store needed.
//
// The per-client bucket map is never proactively swept: for a self-hosted
// tool's realistic traffic (a handful of real users, not a flood of unique
// attacker IPs), the number of distinct clients seen over a process's
// lifetime stays small enough that this is a non-issue in practice.
func RateLimit(maxRequests int, window time.Duration, trustProxyHeaders bool) func(http.Handler) http.Handler {
	type bucket struct {
		count     int
		windowEnd time.Time
	}
	var (
		mu      sync.Mutex
		buckets = make(map[string]*bucket)
	)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := clientIP(r, trustProxyHeaders)
			now := time.Now()

			mu.Lock()
			b, ok := buckets[key]
			if !ok || now.After(b.windowEnd) {
				b = &bucket{windowEnd: now.Add(window)}
				buckets[key] = b
			}
			b.count++
			exceeded := b.count > maxRequests
			retryAfter := int(time.Until(b.windowEnd).Seconds()) + 1
			mu.Unlock()

			if exceeded {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				http.Error(w, "too many requests, please try again later", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the request's client IP, trusting X-Forwarded-For/
// X-Real-IP only when trustProxyHeaders is set — the same trust decision
// already used throughout Victus for cookies/HSTS (see server.go's
// `secure` variable), and the same reasoning documented there for not
// using chi's RealIP middleware unconditionally: blindly trusting these
// headers lets a client spoof the IP it's rate-limited under.
func clientIP(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Leftmost entry is the original client per the de-facto
			// X-Forwarded-For convention.
			if i := strings.IndexByte(xff, ','); i != -1 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
		if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
			return xrip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

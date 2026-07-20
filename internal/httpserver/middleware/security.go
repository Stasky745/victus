// Package middleware holds cross-cutting HTTP middleware for Victus.
package middleware

import "net/http"

// SecurityHeaders returns middleware that sets a conservative baseline of
// security headers on every response. secure should be true whenever
// Victus is served over HTTPS (directly or behind a trusted reverse proxy,
// i.e. the same signal used to decide whether cookies get the Secure flag)
// — HSTS is only meaningful, and only advertised, in that case.
func SecurityHeaders(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "same-origin")
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'")
			if secure || r.TLS != nil {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

package auth

import (
	"net/http"
	"time"
)

// SetCookie sets an HttpOnly, SameSite=Lax cookie that expires after ttl.
// secure should be true whenever Victus is served over HTTPS (directly or
// behind a trusted reverse proxy) — see Config.TrustProxyHeaders.
func SetCookie(w http.ResponseWriter, name, value, path string, ttl time.Duration, secure bool) {
	//nolint:gosec // secure is threaded from cfg.TrustProxyHeaders; false only for local plain-HTTP dev.
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

// ClearCookie deletes a cookie previously set by SetCookie.
func ClearCookie(w http.ResponseWriter, name, path string, secure bool) {
	//nolint:gosec // secure is threaded from cfg.TrustProxyHeaders; false only for local plain-HTTP dev.
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

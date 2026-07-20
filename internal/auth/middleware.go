package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/Stasky745/victus/internal/httperr"
)

type ctxKey int

const userCtxKey ctxKey = iota

// RequireAuth resolves the session on every request; unauthenticated requests
// are redirected to /login. On success, the User is attached to the request
// context. A database error while resolving the session (as opposed to
// simply having no session) is reported as 500, not treated as a logout.
func RequireAuth(sessions *SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := sessions.Resolve(r)
			if err != nil {
				if errors.Is(err, ErrNoSession) {
					http.Redirect(w, r, "/login", http.StatusSeeOther)
					return
				}
				httperr.Internal(w, r, "failed to resolve session", err)
				return
			}
			ctx := context.WithValue(r.Context(), userCtxKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserFromContext returns the authenticated user attached by RequireAuth.
// Only call this on routes mounted behind RequireAuth.
func UserFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(userCtxKey).(*User)
	return u
}

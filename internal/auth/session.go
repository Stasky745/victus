package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/db/sqlc"
)

const (
	sessionCookieName = "victus_session"
	sessionTTL        = 30 * 24 * time.Hour
)

// ErrNoSession is returned when the request carries no session cookie, an
// unparseable one, or one that doesn't match a live (non-expired) session
// row. It does NOT cover database errors — those are returned as-is so
// callers can tell "not logged in" apart from "the DB is having a bad time".
var ErrNoSession = errors.New("no valid session")

// User is the authenticated identity attached to request context.
type User struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	IsAdmin     bool
}

// SessionManager creates, resolves, and clears Victus's server-side sessions.
type SessionManager struct {
	q      sqlc.Querier
	secure bool // false only in local (non-TLS) development
}

// NewSessionManager returns a SessionManager backed by q. secureCookies
// should be true whenever Victus is served over HTTPS (directly or behind
// a trusted reverse proxy).
func NewSessionManager(q sqlc.Querier, secureCookies bool) *SessionManager {
	return &SessionManager{q: q, secure: secureCookies}
}

// StartSession creates a session row for userID and sets the session cookie.
func (m *SessionManager) StartSession(ctx context.Context, w http.ResponseWriter, userID uuid.UUID) error {
	session, err := m.q.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:        uuid.New(),
		UserID:    userID,
		ExpiresAt: time.Now().Add(sessionTTL),
	})
	if err != nil {
		return err
	}
	SetCookie(w, sessionCookieName, session.ID.String(), "/", sessionTTL, m.secure)
	return nil
}

// Resolve reads the session cookie from r and returns the authenticated
// user, if any. A database error while looking up the session is returned
// unwrapped (not as ErrNoSession) so RequireAuth can distinguish "not
// logged in" from "couldn't check" and avoid treating a DB blip as a
// mass logout.
func (m *SessionManager) Resolve(r *http.Request) (*User, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, ErrNoSession
	}
	id, err := uuid.Parse(cookie.Value)
	if err != nil {
		return nil, ErrNoSession
	}
	row, err := m.q.GetSession(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoSession
		}
		return nil, fmt.Errorf("look up session: %w", err)
	}
	return &User{
		ID:          row.UserID,
		Email:       row.Email,
		DisplayName: row.DisplayName.String,
		IsAdmin:     row.IsAdmin,
	}, nil
}

// EndSession deletes the session server-side and clears the cookie. The
// cookie is always cleared even if the server-side delete fails — a failed
// delete is logged rather than swallowed, since silently ignoring it would
// leave the session valid server-side with no client-visible sign the
// logout didn't fully take effect.
func (m *SessionManager) EndSession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if id, err := uuid.Parse(cookie.Value); err == nil {
			if err := m.q.DeleteSession(r.Context(), id); err != nil {
				slog.ErrorContext(r.Context(), "failed to delete session during logout", "error", err)
			}
		}
	}
	ClearCookie(w, sessionCookieName, "/", m.secure)
}

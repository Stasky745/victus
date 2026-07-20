package auth_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/auth"
	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/dbtest"
)

func newTestUser(t *testing.T, q sqlc.Querier) sqlc.User {
	t.Helper()
	suffix := t.Name() + "-" + uuid.NewString()
	user, err := q.CreateUser(context.Background(), sqlc.CreateUserParams{
		ID:          uuid.New(),
		OidcSubject: sql.NullString{String: "test-subject-" + suffix, Valid: true},
		Email:       "test-" + suffix + "@example.com",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func newTestQuerier(t *testing.T) sqlc.Querier {
	t.Helper()
	sqlDB := dbtest.NewDB(t)
	q, err := db.NewQuerier(dbtest.Driver(), sqlDB)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	return q
}

func TestSessionManager_StartResolveEnd(t *testing.T) {
	ctx := t.Context()
	q := newTestQuerier(t)
	user := newTestUser(t, q)

	sessions := auth.NewSessionManager(q, false)

	// Start a session and capture the cookie it sets.
	rec := httptest.NewRecorder()
	if err := sessions.StartSession(ctx, rec, user.ID); err != nil {
		t.Fatalf("start session: %v", err)
	}
	resp := rec.Result()
	defer func() { _ = resp.Body.Close() }()
	if len(resp.Cookies()) == 0 {
		t.Fatal("expected a session cookie to be set")
	}
	cookie := resp.Cookies()[0]
	if !cookie.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Error("session cookie must be SameSite=Lax")
	}

	// Resolve it back on a fresh request carrying the cookie.
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	resolved, err := sessions.Resolve(req)
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if resolved.Email != user.Email {
		t.Errorf("resolved email = %q, want %q", resolved.Email, user.Email)
	}

	// End it and confirm it no longer resolves.
	endRec := httptest.NewRecorder()
	sessions.EndSession(endRec, req)

	req2 := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	req2.AddCookie(cookie)
	if _, err := sessions.Resolve(req2); err == nil {
		t.Fatal("expected resolving a session after EndSession to fail")
	}
}

func TestSessionManager_Resolve_NoCookie(t *testing.T) {
	sessions := auth.NewSessionManager(newTestQuerier(t), false)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	if _, err := sessions.Resolve(req); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("expected ErrNoSession, got %v", err)
	}
}

func TestSessionManager_Resolve_GarbageCookie(t *testing.T) {
	sessions := auth.NewSessionManager(newTestQuerier(t), false)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "victus_session", Value: "not-a-uuid"})
	if _, err := sessions.Resolve(req); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("expected ErrNoSession for a malformed cookie, got %v", err)
	}
}

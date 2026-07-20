package httpserver_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Stasky745/victus/internal/config"
	"github.com/Stasky745/victus/internal/dbtest"
	"github.com/Stasky745/victus/internal/httpserver"
)

// newTestServer builds a real router against a throwaway database (whichever
// backend TEST_DB_DRIVER selects). OIDC discovery is skipped (nil
// Authenticator) since these tests only exercise routes that don't require a
// live IdP: health checks, static assets, and unauthenticated-route
// CSRF/redirect behavior.
func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	srv, _ := newTestServerAndPool(t)
	return srv
}

// newTestServerAndPool is like newTestServer but also returns the *sql.DB
// backing it, for tests that need to seed data or open their own session
// (e.g. authenticated-route tests via testClient).
func newTestServerAndPool(t *testing.T) (http.Handler, *sql.DB) {
	t.Helper()
	return newTestServerAndPoolWithOptions(t)
}

// newTestServerAndPoolWithOptions is newTestServerAndPool plus the ability
// to inject fake importer clients (httpserver.WithOFFClient,
// httpserver.WithMealieClient) pointed at an httptest.Server, so import
// tests never depend on the real Mealie/Open Food Facts services.
func newTestServerAndPoolWithOptions(t *testing.T, opts ...httpserver.Option) (http.Handler, *sql.DB) {
	t.Helper()
	sqlDB := dbtest.NewDB(t)

	cfg := &config.Config{
		HTTPAddr:      ":8080",
		BaseURL:       "http://localhost:8080",
		DBDriver:      dbtest.Driver(),
		DatabaseURL:   "unused-db-already-open",
		SessionSecret: "01234567890123456789012345678901",
	}
	srv, err := httpserver.New(cfg, sqlDB, nil, opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv, sqlDB
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadyz_DatabaseUp(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestIndex_RedirectsToLoginWhenUnauthenticated(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d (redirect to /login)", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestStaticAssets_Served(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/app.css", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("expected X-Content-Type-Options header on every response")
	}
}

// TestLogin_RateLimited confirms /login is actually wired up with the rate
// limiter in the full router — internal/httpserver/middleware's own tests
// already cover the middleware's logic in isolation; this guards against it
// being registered on the wrong route group, or not applied at all. This
// test's Server has a nil OIDC authenticator (newTestServer avoids routes
// needing a live IdP), so /login itself 500s past the rate limiter — that's
// fine here, since only the limiter's own behavior (never blocking the
// first request, eventually blocking a flood) is under test.
func TestLogin_RateLimited(t *testing.T) {
	srv := newTestServer(t)

	req := func() *http.Request {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
		r.RemoteAddr = "203.0.113.9:12345"
		return r
	}

	first := httptest.NewRecorder()
	srv.ServeHTTP(first, req())
	if first.Code == http.StatusTooManyRequests {
		t.Fatal("the very first request must not be rate-limited")
	}

	var sawTooManyRequests bool
	for range 30 {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req())
		if rec.Code == http.StatusTooManyRequests {
			sawTooManyRequests = true
			break
		}
	}
	if !sawTooManyRequests {
		t.Error("expected repeated requests to /login to eventually be rate-limited (429), got none in 30 attempts")
	}
}

func TestLogout_RequiresCSRFToken(t *testing.T) {
	srv := newTestServer(t)

	// A bare cross-origin-style POST with no CSRF cookie/token must be
	// rejected — /logout is a state-changing route and must not be the one
	// mutating endpoint exempt from CSRF protection.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/logout", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (CSRF rejection)", rec.Code, http.StatusForbidden)
	}
}

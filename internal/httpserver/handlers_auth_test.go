package httpserver_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/auth"
	"github.com/Stasky745/victus/internal/config"
	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/dbtest"
	"github.com/Stasky745/victus/internal/httpserver"
)

// authenticatedClientAs starts a real session for an already-existing user
// (rather than creating a fresh OIDC-style one, as newAuthenticatedClient
// does) — used for password-mode admin/non-admin tests where the account's
// specific privileges matter.
func authenticatedClientAs(t *testing.T, sqlDB *sql.DB, srv http.Handler, user sqlc.User) *testClient {
	t.Helper()
	q, err := db.NewQuerier(dbtest.Driver(), sqlDB)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	sessions := auth.NewSessionManager(q, false)
	rec := httptest.NewRecorder()
	if err := sessions.StartSession(t.Context(), rec, user.ID); err != nil {
		t.Fatalf("start session: %v", err)
	}
	c := &testClient{t: t, srv: srv, cookies: map[string]*http.Cookie{}}
	c.absorb(rec.Result())
	return c
}

const (
	testAdminEmail    = "admin@example.com"
	testAdminPassword = "correct-horse-battery-staple"
)

// newPasswordAuthTestServer builds a real router with AUTH_MODE=password —
// the mode most tests in this package don't exercise (newTestServer's OIDC
// mode covers those instead) — bootstrapping the admin account the same way
// cmdServe does in cmd/victus/cli.go, so these tests catch drift between the
// two if that wiring ever diverges.
func newPasswordAuthTestServer(t *testing.T) (http.Handler, *sql.DB) {
	t.Helper()
	sqlDB := dbtest.NewDB(t)
	q, err := db.NewQuerier(dbtest.Driver(), sqlDB)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}

	if err := auth.EnsureAdminUser(t.Context(), q, testAdminEmail, testAdminPassword); err != nil {
		t.Fatalf("bootstrap admin user: %v", err)
	}

	cfg := &config.Config{
		HTTPAddr:      ":8080",
		BaseURL:       "http://localhost:8080",
		DBDriver:      dbtest.Driver(),
		DatabaseURL:   "unused-db-already-open",
		SessionSecret: "01234567890123456789012345678901",
		AuthMode:      "password",
		AdminEmail:    testAdminEmail,
		AdminPassword: testAdminPassword,
	}
	srv, err := httpserver.New(cfg, sqlDB, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv, sqlDB
}

func TestLoginPage_RendersFormInPasswordMode(t *testing.T) {
	srv, _ := newPasswordAuthTestServer(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `name="password"`) {
		t.Error("expected the login page to render a password field")
	}
}

func TestLoginSubmit_CorrectCredentials_StartsSession(t *testing.T) {
	srv, _ := newPasswordAuthTestServer(t)
	c := &passwordLoginClient{t: t, srv: srv, cookies: map[string]*http.Cookie{}}

	rec := c.login(testAdminEmail, testAdminPassword)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if _, ok := c.cookies["victus_session"]; !ok {
		t.Error("expected a session cookie to be set after a successful login")
	}

	// The session must actually work against an authenticated route. GET /
	// redirects an authenticated user to today's day view (see handleToday)
	// rather than rendering directly — a 303 here (not a redirect back to
	// /login) is the signal that the session was accepted.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	homeRec := httptest.NewRecorder()
	srv.ServeHTTP(homeRec, req)
	if homeRec.Code != http.StatusSeeOther || homeRec.Header().Get("Location") == "/login" {
		t.Errorf("authenticated request to / after login: status = %d, location = %q, want a redirect to a day view, not /login",
			homeRec.Code, homeRec.Header().Get("Location"))
	}
}

func TestLoginSubmit_WrongPassword_Rejected(t *testing.T) {
	srv, _ := newPasswordAuthTestServer(t)
	c := &passwordLoginClient{t: t, srv: srv, cookies: map[string]*http.Cookie{}}

	rec := c.login(testAdminEmail, "not-the-password")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if _, ok := c.cookies["victus_session"]; ok {
		t.Error("no session cookie should be set after a failed login")
	}
}

func TestLoginSubmit_UnknownEmail_Rejected(t *testing.T) {
	srv, _ := newPasswordAuthTestServer(t)
	c := &passwordLoginClient{t: t, srv: srv, cookies: map[string]*http.Cookie{}}

	rec := c.login("nobody@example.com", "whatever")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestSettingsCreateUser_AdminCanCreate(t *testing.T) {
	srv, pool := newPasswordAuthTestServer(t)
	q, err := db.NewQuerier(dbtest.Driver(), pool)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	admin, err := q.GetUserByEmail(t.Context(), testAdminEmail)
	if err != nil {
		t.Fatalf("get bootstrapped admin: %v", err)
	}
	c := authenticatedClientAs(t, pool, srv, admin)

	token := c.csrfToken("/settings")
	rec := c.postForm("/settings/users", url.Values{
		"email":        {"newuser@example.com"},
		"display_name": {"New User"},
		"password":     {"another-strong-password"},
	}, token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	created, err := q.GetUserByEmail(t.Context(), "newuser@example.com")
	if err != nil {
		t.Fatalf("expected the new user to exist: %v", err)
	}
	if created.IsAdmin {
		t.Error("a user created from Settings must not be an admin")
	}
	if !auth.CheckPassword(created.PasswordHash.String, "another-strong-password") {
		t.Error("the new user's password doesn't match what was submitted")
	}
}

// TestSettingsCreateUser_NonAdminForbidden guards the actual security
// boundary: without this check, any logged-in user (not just admins) could
// create arbitrary accounts on the instance, including other admins.
func TestSettingsCreateUser_NonAdminForbidden(t *testing.T) {
	srv, pool := newPasswordAuthTestServer(t)
	q, err := db.NewQuerier(dbtest.Driver(), pool)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}

	hash, err := auth.HashPassword("some-password-123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	nonAdmin, err := q.CreateUserWithPassword(t.Context(), sqlc.CreateUserWithPasswordParams{
		ID:           uuid.New(),
		Email:        "regular@example.com",
		PasswordHash: sql.NullString{String: hash, Valid: true},
	})
	if err != nil {
		t.Fatalf("create non-admin user: %v", err)
	}
	c := authenticatedClientAs(t, pool, srv, nonAdmin)

	token := c.csrfToken("/settings")
	rec := c.postForm("/settings/users", url.Values{
		"email":    {"sneaky@example.com"},
		"password": {"whatever-password"},
	}, token)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if _, err := q.GetUserByEmail(t.Context(), "sneaky@example.com"); err == nil {
		t.Error("no user should have been created by a non-admin's request")
	}
}

// passwordLoginClient is a minimal cookie-carrying client for the
// GET-then-POST /login round trip (fetch the CSRF token, then submit
// credentials) — testClient (testclient_test.go) assumes an already
// authenticated session, which doesn't fit the login flow itself.
type passwordLoginClient struct {
	t       *testing.T
	srv     http.Handler
	cookies map[string]*http.Cookie
}

func (c *passwordLoginClient) absorb(resp *http.Response) {
	for _, ck := range resp.Cookies() {
		c.cookies[ck.Name] = ck
	}
}

func (c *passwordLoginClient) login(email, password string) *httptest.ResponseRecorder {
	c.t.Helper()

	getReq := httptest.NewRequestWithContext(c.t.Context(), http.MethodGet, "/login", nil)
	getRec := httptest.NewRecorder()
	c.srv.ServeHTTP(getRec, getReq)
	c.absorb(getRec.Result())

	m := csrfFieldRe.FindStringSubmatch(getRec.Body.String())
	if m == nil {
		c.t.Fatalf("could not find a CSRF token on the login page (status %d)", getRec.Code)
	}
	token := m[1]

	form := url.Values{"email": {email}, "password": {password}, "gorilla.csrf.Token": {token}}
	postReq := httptest.NewRequestWithContext(c.t.Context(), http.MethodPost, "/login", strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Host = "localhost:8080"
	postReq.Header.Set("Referer", "http://localhost:8080/login")
	for _, ck := range c.cookies {
		postReq.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	c.srv.ServeHTTP(rec, postReq)
	c.absorb(rec.Result())
	return rec
}

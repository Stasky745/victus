package httpserver_test

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/auth"
	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/dbtest"
)

var csrfFieldRe = regexp.MustCompile(`name="gorilla\.csrf\.Token" value="([^"]+)"`)

// csrfHeaderRe matches the token embedded in every page's hx-headers
// attribute (set once in layout.Base). The surrounding JSON quotes get
// HTML-attribute-escaped to "&#34;" when rendered, and — critically —
// "&#34;" itself contains the digits 3 and 4, which collide with a naive
// "skip anything that isn't base64" prefix match (it would stop skipping
// right at that "34" and fail to find the real token afterward). Matching
// both possible quote forms ('"' or "&#34;") explicitly around the value
// sidesteps that entirely. csrf.Token(r) returns standard (not URL-safe)
// base64, so the value charset includes '+', '/', and '=' padding.
var csrfHeaderRe = regexp.MustCompile(`X-CSRF-Token(?:&#34;|")?:(?:&#34;|")([A-Za-z0-9+/=]+)(?:&#34;|")`)

// testClient is a minimal cookie-jar-carrying HTTP client for exercising
// authenticated, CSRF-protected routes in tests without going through a
// real OIDC round trip.
type testClient struct {
	t       *testing.T
	srv     http.Handler
	cookies map[string]*http.Cookie
}

// newAuthenticatedClient logs a fresh user in directly via the session
// store (bypassing OIDC, which these tests don't stand up) and returns a
// client that carries that session's cookie on every request.
func newAuthenticatedClient(t *testing.T, sqlDB *sql.DB, srv http.Handler) *testClient {
	t.Helper()
	ctx := t.Context()

	q, err := db.NewQuerier(dbtest.Driver(), sqlDB)
	if err != nil {
		t.Fatalf("new querier: %v", err)
	}
	// uuid-suffixed so a test creating multiple clients (e.g. an owner and
	// an attacker) doesn't collide on oidc_subject's or email's unique
	// constraints.
	suffix := t.Name() + "-" + uuid.NewString()
	user, err := q.CreateUser(ctx, sqlc.CreateUserParams{
		ID:          uuid.New(),
		OidcSubject: sql.NullString{String: "test-subject-" + suffix, Valid: true},
		Email:       "test-" + suffix + "@example.com",
	})
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}

	sessions := auth.NewSessionManager(q, false)
	rec := httptest.NewRecorder()
	if err := sessions.StartSession(ctx, rec, user.ID); err != nil {
		t.Fatalf("start session: %v", err)
	}

	c := &testClient{t: t, srv: srv, cookies: map[string]*http.Cookie{}}
	c.absorb(rec.Result())
	return c
}

func (c *testClient) absorb(resp *http.Response) {
	for _, ck := range resp.Cookies() {
		c.cookies[ck.Name] = ck
	}
}

func (c *testClient) do(method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequestWithContext(c.t.Context(), method, path, body)
	// gorilla/csrf validates Origin/Referer on unsafe methods regardless of
	// scheme; a same-origin Referer matching the test server's BaseURL
	// mirrors what a real browser sends. httptest.NewRequest defaults Host
	// to "example.com", so it must be overridden to match, or gorilla/csrf
	// sees a same-Referer-different-Host mismatch and rejects it anyway.
	req.Host = "localhost:8080"
	req.Header.Set("Referer", "http://localhost:8080"+path)
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	c.srv.ServeHTTP(rec, req)
	c.absorb(rec.Result())
	return rec
}

func (c *testClient) get(path string) *httptest.ResponseRecorder {
	return c.do(http.MethodGet, path, nil, nil)
}

// csrfToken performs a GET against any page and extracts a valid CSRF
// token, picking up the CSRF cookie along the way. The extracted token
// remains valid for subsequent requests as long as the CSRF cookie
// captured here is still attached. Tries the hidden form field first (more
// specific), then falls back to the hx-headers attribute every page sets.
func (c *testClient) csrfToken(pagePath string) string {
	c.t.Helper()
	rec := c.get(pagePath)
	body := rec.Body.String()
	if m := csrfFieldRe.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	if m := csrfHeaderRe.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	c.t.Fatalf("could not find a CSRF token in the response from %s (status %d)", pagePath, rec.Code)
	return ""
}

func (c *testClient) postForm(path string, form url.Values, token string) *httptest.ResponseRecorder {
	form = cloneValues(form)
	form.Set("gorilla.csrf.Token", token)
	return c.do(http.MethodPost, path, strings.NewReader(form.Encode()), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	})
}

// postFormHX is postForm plus the HX-Request header a real htmx request
// carries — needed for routes like POST /days/{date}/items, which branch on
// it to tell the Day Builder's htmx-fragment "Add" button apart from the
// Week Builder's plain-form-submit "Add" button hitting the same endpoint.
func (c *testClient) postFormHX(path string, form url.Values, token string) *httptest.ResponseRecorder {
	form = cloneValues(form)
	form.Set("gorilla.csrf.Token", token)
	return c.do(http.MethodPost, path, strings.NewReader(form.Encode()), map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"HX-Request":   "true",
	})
}

func (c *testClient) delete(path, token string) *httptest.ResponseRecorder {
	return c.do(http.MethodDelete, path, nil, map[string]string{
		"X-CSRF-Token": token,
	})
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestAllMutatingRoutesRequireCSRF is a structural guardrail, not just a
// point fix: it walks every registered route and, for each unsafe-method
// (POST/PUT/PATCH/DELETE) route, confirms CSRF is actually enforced. If a
// future route is ever registered outside the CSRF-protected groups — the
// exact mistake that let POST /logout ship without CSRF protection — this
// test fails instead of silently shipping an unprotected mutating endpoint.
//
// Two checks per route:
//  1. Authenticated, no CSRF token: must be rejected (403). This is the
//     real guarantee — proves CSRF is enforced once a client is past auth,
//     which is where an actual CSRF attack targets a logged-in victim.
//  2. Unauthenticated, no CSRF token: must NOT succeed. Routes behind
//     RequireAuth correctly redirect to /login here (auth runs before CSRF
//     by design — see the ordering comment in server.go) rather than
//     returning 403; /logout has no auth gate so it must return 403
//     directly. Either outcome is fine as long as the action never runs.
func TestAllMutatingRoutesRequireCSRF(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	authed := newAuthenticatedClient(t, pool, srv)

	routable, ok := srv.(chi.Routes)
	if !ok {
		t.Fatal("router does not implement chi.Routes; can't walk its routes")
	}

	unsafeMethods := map[string]bool{
		http.MethodPost:   true,
		http.MethodPut:    true,
		http.MethodPatch:  true,
		http.MethodDelete: true,
	}

	// Routes registered via r.Handle (method-agnostic, e.g. static asset
	// serving) show up under every method during a Walk even though they
	// never mutate state — file serving is read-only regardless of verb.
	// Any new entry here must be justified the same way.
	exempt := map[string]bool{
		"/static/*": true,
	}

	var checked int
	err := chi.Walk(routable, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !unsafeMethods[method] || exempt[route] {
			return nil
		}
		checked++

		// Check 1: authenticated, no token.
		authedRec := authed.do(method, route, nil, nil)
		if authedRec.Code != http.StatusForbidden {
			t.Errorf("%s %s: authenticated request with no CSRF token: expected 403, got %d — "+
				"this route may have been registered outside CSRF protection", method, route, authedRec.Code)
		}

		// Check 2: unauthenticated, no token — must not let the action run.
		req := httptest.NewRequestWithContext(t.Context(), method, route, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		loggedInRedirect := rec.Code == http.StatusSeeOther && rec.Header().Get("Location") == "/login"
		if rec.Code != http.StatusForbidden && !loggedInRedirect {
			t.Errorf("%s %s: unauthenticated request with no CSRF token: expected 403 or a redirect to "+
				"/login, got %d — this route may have been registered outside CSRF protection", method, route, rec.Code)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	if checked == 0 {
		t.Fatal("no mutating routes found to walk — if that's genuinely true, update this test's assumptions")
	}
}

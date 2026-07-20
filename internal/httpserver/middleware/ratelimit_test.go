package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appmw "github.com/Stasky745/victus/internal/httpserver/middleware"
)

func TestRateLimit_AllowsUpToMax(t *testing.T) {
	handler := appmw.RateLimit(3, time.Minute, false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := range 3 {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
		req.RemoteAddr = "203.0.113.1:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want %d", i+1, rec.Code, http.StatusOK)
		}
	}
}

func TestRateLimit_BlocksOverMax(t *testing.T) {
	handler := appmw.RateLimit(3, time.Minute, false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for range 3 {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
		req.RemoteAddr = "203.0.113.2:12345"
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
	req.RemoteAddr = "203.0.113.2:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected a Retry-After header on a rate-limited response")
	}
}

// TestRateLimit_TracksClientsIndependently guards against a real bug class:
// a shared/global counter (instead of one per client) would let one
// aggressive client exhaust the limit for every other user of the app.
func TestRateLimit_TracksClientsIndependently(t *testing.T) {
	handler := appmw.RateLimit(1, time.Minute, false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
	first.RemoteAddr = "203.0.113.3:1"
	handler.ServeHTTP(httptest.NewRecorder(), first)

	// A different client hitting the same limiter must not be affected by
	// the first client's usage.
	second := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
	second.RemoteAddr = "203.0.113.4:1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, second)

	if rec.Code != http.StatusOK {
		t.Errorf("a different client's request: status = %d, want %d (independent limits)", rec.Code, http.StatusOK)
	}
}

func TestRateLimit_ResetsAfterWindow(t *testing.T) {
	const window = 30 * time.Millisecond
	handler := appmw.RateLimit(1, window, false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := func() *http.Request {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
		r.RemoteAddr = "203.0.113.5:1"
		return r
	}

	handler.ServeHTTP(httptest.NewRecorder(), req())
	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, req())
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("second request within the window: status = %d, want %d", blocked.Code, http.StatusTooManyRequests)
	}

	time.Sleep(window * 2)

	afterReset := httptest.NewRecorder()
	handler.ServeHTTP(afterReset, req())
	if afterReset.Code != http.StatusOK {
		t.Errorf("request after the window elapsed: status = %d, want %d", afterReset.Code, http.StatusOK)
	}
}

// TestRateLimit_IgnoresForwardedHeaderWhenNotTrusted guards the same
// spoofing concern server.go's own comment documents for chi's RealIP
// middleware: without an explicitly trusted proxy, a client could put any
// value in X-Forwarded-For to dodge its real rate limit.
func TestRateLimit_IgnoresForwardedHeaderWhenNotTrusted(t *testing.T) {
	handler := appmw.RateLimit(1, time.Minute, false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	newReq := func(forwardedFor string) *http.Request {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
		r.RemoteAddr = "203.0.113.6:1"
		r.Header.Set("X-Forwarded-For", forwardedFor)
		return r
	}

	handler.ServeHTTP(httptest.NewRecorder(), newReq("1.1.1.1"))
	// Same real RemoteAddr, a different spoofed X-Forwarded-For — since
	// trustProxyHeaders is false, this must still count against the same
	// bucket as the first request (keyed by RemoteAddr, not the header).
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newReq("2.2.2.2"))

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d — X-Forwarded-For must be ignored when trustProxyHeaders is false", rec.Code, http.StatusTooManyRequests)
	}
}

// TestRateLimit_HonorsForwardedHeaderWhenTrusted is the flip side: once an
// operator has explicitly opted into trusting their reverse proxy, distinct
// clients behind that proxy (sharing one RemoteAddr) must get independent
// limits, keyed by the proxy-supplied X-Forwarded-For value.
func TestRateLimit_HonorsForwardedHeaderWhenTrusted(t *testing.T) {
	handler := appmw.RateLimit(1, time.Minute, true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	newReq := func(forwardedFor string) *http.Request {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
		r.RemoteAddr = "10.0.0.1:1" // the trusted proxy's own address, shared by every real client
		r.Header.Set("X-Forwarded-For", forwardedFor)
		return r
	}

	handler.ServeHTTP(httptest.NewRecorder(), newReq("198.51.100.1"))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newReq("198.51.100.2"))
	if rec.Code != http.StatusOK {
		t.Errorf("a different X-Forwarded-For client: status = %d, want %d (independent limits behind a trusted proxy)", rec.Code, http.StatusOK)
	}
}

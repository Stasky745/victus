// Package httpserver assembles Victus's chi router: middleware, health checks,
// authentication routes, and (eventually) the day/week/meal/config feature routes.
package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/csrf"

	"github.com/Stasky745/victus/internal/auth"
	"github.com/Stasky745/victus/internal/config"
	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/exportimport"
	"github.com/Stasky745/victus/internal/goals"
	appmw "github.com/Stasky745/victus/internal/httpserver/middleware"
	"github.com/Stasky745/victus/internal/importers/mealie"
	"github.com/Stasky745/victus/internal/importers/openfoodfacts"
	"github.com/Stasky745/victus/internal/mealslib"
	"github.com/Stasky745/victus/internal/planning"
	"github.com/Stasky745/victus/web"
)

const pingTimeout = 2 * time.Second

// authRateLimitMax/authRateLimitWindow bound how often one client can hit
// /login or /auth/callback together — blunts credential/OIDC-callback abuse
// (repeated login attempts, authorization-code guessing) per the project's
// security checklist. Generous enough for legitimate retries (a typo'd
// IdP password, multiple tabs) while still meaningfully slowing automated
// abuse; a shared counter across both routes since one real login always
// hits both.
const (
	authRateLimitMax    = 20
	authRateLimitWindow = 5 * time.Minute
)

// Server holds the dependencies needed by HTTP handlers.
type Server struct {
	cfg                *config.Config
	db                 *sql.DB
	queries            sqlc.Querier
	oidc               *auth.Authenticator
	sessions           *auth.SessionManager
	meals              *mealslib.Store
	planning           *planning.Store
	goals              *goals.Store
	exportimport       *exportimport.Store
	off                *openfoodfacts.Client
	mealie             *mealie.Client // nil when MEALIE_BASE_URL isn't configured
	adminAllowedEmails []string
	secureCookies      bool
	passwordAuth       bool // true when cfg.AuthMode == "password"; false means OIDC
}

// Option configures optional Server dependencies — production code never
// needs these; tests use them to inject fake importer clients pointed at
// an httptest.Server instead of the real Mealie/Open Food Facts services.
type Option func(*Server)

// WithOFFClient overrides the Open Food Facts client New would otherwise
// construct.
func WithOFFClient(c *openfoodfacts.Client) Option {
	return func(s *Server) { s.off = c }
}

// WithMealieClient overrides the Mealie client, regardless of whether
// cfg.MealieConfigured() would normally enable one.
func WithMealieClient(c *mealie.Client) Option {
	return func(s *Server) { s.mealie = c }
}

// New builds the full chi router for Victus.
func New(cfg *config.Config, sqlDB *sql.DB, oidcAuth *auth.Authenticator, opts ...Option) (http.Handler, error) {
	// The single source of truth for "should cookies/headers behave as if
	// this is HTTPS": true if Victus is directly TLS-terminated (checked
	// per-request via r.TLS in SecurityHeaders) or sits behind a reverse
	// proxy the operator has explicitly told us to trust.
	secure := cfg.TrustProxyHeaders

	q, err := db.NewQuerier(cfg.DBDriver, sqlDB)
	if err != nil {
		return nil, fmt.Errorf("build querier: %w", err)
	}
	mealsStore, err := mealslib.New(sqlDB, cfg.DBDriver)
	if err != nil {
		return nil, fmt.Errorf("build meals store: %w", err)
	}
	planningStore, err := planning.New(sqlDB, cfg.DBDriver)
	if err != nil {
		return nil, fmt.Errorf("build planning store: %w", err)
	}
	goalsStore, err := goals.New(sqlDB, cfg.DBDriver)
	if err != nil {
		return nil, fmt.Errorf("build goals store: %w", err)
	}
	exportimportStore, err := exportimport.New(sqlDB, cfg.DBDriver)
	if err != nil {
		return nil, fmt.Errorf("build exportimport store: %w", err)
	}

	s := &Server{
		cfg:                cfg,
		db:                 sqlDB,
		queries:            q,
		oidc:               oidcAuth,
		sessions:           auth.NewSessionManager(q, secure),
		meals:              mealsStore,
		planning:           planningStore,
		goals:              goalsStore,
		exportimport:       exportimportStore,
		off:                openfoodfacts.New(),
		adminAllowedEmails: cfg.AdminAllowedEmails,
		secureCookies:      secure,
		passwordAuth:       cfg.PasswordAuth(),
	}
	if cfg.MealieConfigured() {
		s.mealie = mealie.New(cfg.MealieBaseURL, cfg.MealieAPIKey)
	}
	for _, opt := range opts {
		opt(s)
	}

	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	// Deliberately not using chi's RealIP middleware: it blindly trusts
	// X-Forwarded-For/X-Real-IP without validating a trusted-hop count,
	// which lets clients spoof their logged IP. Bring your own middleware
	// here if you need accurate client IPs behind a specific known proxy.
	r.Use(appmw.RequestLogger)
	r.Use(chimw.Recoverer)
	r.Use(appmw.SecurityHeaders(secure))

	// Static assets (compiled CSS, vendored htmx) — served directly from the embedded FS.
	staticFS, _ := fs.Sub(web.StaticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	csrfMiddleware := csrf.Protect(
		[]byte(cfg.SessionSecret),
		csrf.Secure(secure),
		csrf.Path("/"),
		csrf.ErrorHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			slog.WarnContext(r.Context(), "csrf validation failed",
				"request_id", chimw.GetReqID(r.Context()),
				"reason", csrf.FailureReason(r),
			)
			http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
		})),
	)

	// IMPORTANT: every mutating (POST/PUT/PATCH/DELETE) route must sit inside
	// a group that applies csrfMiddleware — a route registered directly on
	// `r` outside these groups ships with no CSRF protection. This isn't
	// just a comment: TestAllMutatingRoutesRequireCSRF (csrf_coverage_test.go)
	// walks the whole route tree and fails CI if that ever happens again.

	// Auth routes are unauthenticated by definition. CSRF-protected even
	// though /login (GET) itself doesn't mutate anything — it's the route
	// that hands out the token used by the POST below in password mode.
	r.Group(func(r chi.Router) {
		r.Use(appmw.RateLimit(authRateLimitMax, authRateLimitWindow, secure))
		if !secure {
			r.Use(markPlaintextHTTP)
		}
		r.Use(csrfMiddleware)
		r.Get("/login", s.handleLogin)
		if s.passwordAuth {
			r.Post("/login", s.handleLoginSubmit)
		} else {
			r.Get("/auth/callback", s.handleAuthCallback)
		}
	})

	// /logout is a state-changing (POST) route but deliberately not behind
	// RequireAuth — logging out with an already-expired session must still
	// succeed — so it gets its own CSRF-protected group rather than sharing
	// the authenticated group below.
	r.Group(func(r chi.Router) {
		if !secure {
			r.Use(markPlaintextHTTP)
		}
		r.Use(csrfMiddleware)
		r.Post("/logout", s.handleLogout)
	})

	r.Group(func(r chi.Router) {
		// RequireAuth before csrfMiddleware: an unauthenticated request to a
		// mutating route should be redirected to /login, not rejected with a
		// raw CSRF 403 (which would happen if csrf ran first, since a client
		// that never did a GET here yet has no CSRF cookie either way).
		r.Use(auth.RequireAuth(s.sessions))
		if !secure {
			r.Use(markPlaintextHTTP)
		}
		r.Use(csrfMiddleware)

		r.Get("/", s.handleToday)
		r.Get("/days/{date}", s.handleDayBuilder)
		r.Get("/days/{date}/meal-search", s.handleDayMealSearch)
		r.Post("/days/{date}/meal-search/{meal_id}/favorite", s.handleDayMealFavoriteToggle)
		r.Post("/days/{date}/items", s.handleDayAddItem)
		r.Patch("/days/{date}/items/{item_id}", s.handleDayUpdateItemQuantity)
		r.Delete("/days/{date}/items/{item_id}", s.handleDayRemoveItem)

		r.Get("/weeks", s.handleWeekToday)
		r.Get("/weeks/{week_start}", s.handleWeekBuilder)
		r.Get("/weeks/{week_start}/days/{date}/meal-search", s.handleWeekMealSearch)
		r.Post("/weeks/{week_start}/copy", s.handleWeekCopyDay)

		r.Get("/defaults", s.handleDefaultsPage)
		r.Get("/defaults/meal-search", s.handleDefaultsMealSearch)
		r.Post("/defaults/meal-search/{meal_id}/favorite", s.handleDefaultsMealFavoriteToggle)
		r.Post("/defaults/items", s.handleDefaultsAddItem)
		r.Delete("/defaults/items/{item_id}", s.handleDefaultsRemoveItem)

		r.Get("/meals", s.handleMealsList)
		r.Get("/meals/search", s.handleMealsSearch)
		r.Get("/meals/new", s.handleMealNewForm)
		r.Post("/meals", s.handleMealCreate)
		r.Get("/meals/categories", s.handleCategoriesList)
		r.Post("/meals/categories", s.handleCategoryCreate)
		r.Post("/meals/categories/{id}", s.handleCategoryRename)
		r.Post("/meals/categories/{id}/delete", s.handleCategoryDelete)
		r.Get("/meals/labels", s.handleLabelsList)
		r.Post("/meals/labels", s.handleLabelCreate)
		r.Post("/meals/labels/{id}/delete", s.handleLabelDelete)
		r.Get("/meals/{id}/edit", s.handleMealEditForm)
		r.Post("/meals/{id}", s.handleMealUpdate)
		r.Delete("/meals/{id}", s.handleMealDelete)

		r.Get("/meals/import", s.handleImportPage)
		r.Get("/meals/import/mealie/search", s.handleMealieSearch)
		r.Post("/meals/import/mealie/{slug}", s.handleMealieImport)
		r.Get("/meals/import/off/search", s.handleOFFSearch)
		r.Post("/meals/import/off/{barcode}", s.handleOFFImport)

		r.Get("/settings", s.handleSettings)
		r.Post("/settings", s.handleSettingsUpdate)
		r.Post("/settings/info-url", s.handleSettingsUpdateInfoURL)
		r.Post("/settings/export", s.handleExport)
		r.Post("/settings/import", s.handleImport)
		if s.passwordAuth {
			r.Post("/settings/users", s.handleSettingsCreateUser)
		}
	})

	return r, nil
}

// markPlaintextHTTP tells gorilla/csrf this deployment is intentionally
// plain HTTP (no TLS, no trusted proxy). Without it, csrf.Protect
// unconditionally rejects any request whose Referer has an "http" scheme —
// a check meant to stop TLS-downgrade attacks, but which isn't actually
// gated on whether *this* request arrived over TLS. Left unset, that would
// reject legitimate same-origin form submissions from real browsers on any
// plain-HTTP deployment (the default for TRUST_PROXY_HEADERS=false), not
// just an attack.
func markPlaintextHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, csrf.PlaintextHTTPRequest(r))
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), pingTimeout)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		slog.WarnContext(r.Context(), "readyz check failed", "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("database unavailable"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

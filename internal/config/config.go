// Package config loads Victus's configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Stasky745/victus/internal/urlutil"
)

// Config holds all runtime configuration for Victus, sourced entirely from
// environment variables (12-factor style) so the binary carries no
// environment-specific assumptions.
type Config struct {
	// HTTP
	HTTPAddr string
	BaseURL  string
	// TrustProxyHeaders should be true whenever Victus sits behind a
	// trusted, TLS-terminating reverse proxy — it's the sole signal used to
	// decide whether cookies get Secure and whether HSTS is advertised.
	TrustProxyHeaders bool

	// Database. DBDriver is "postgres" (the default) or "sqlite". DatabaseURL
	// is a Postgres DSN in the former case, or a SQLite file path in the
	// latter (e.g. "/data/victus.db").
	DBDriver    string
	DatabaseURL string

	// Auth. AuthMode is "password" (the default — Victus manages its own
	// accounts, no external dependency needed to get running) or "oidc"
	// (delegates entirely to an external IdP; local password login is
	// disabled in that mode).
	AuthMode string

	// Password-mode-only. AdminEmail/AdminPassword bootstrap the first
	// (admin) account on startup — idempotent, so they can stay set across
	// restarts. Additional users are created from Settings by an admin.
	AdminEmail    string
	AdminPassword string

	// OIDC-mode-only.
	OIDCIssuerURL      string
	OIDCClientID       string
	OIDCClientSecret   string
	OIDCRedirectURL    string
	AdminAllowedEmails []string // empty = anyone who authenticates is auto-provisioned

	SessionSecret string

	// Meal importers. Both optional — Victus works fully without either
	// configured, the Meal Library's importer sections just stay hidden.
	MealieBaseURL string // e.g. https://mealie.example.com; no trailing slash
	MealieAPIKey  string

	LogLevel string
}

// Load reads configuration from the process environment and validates it.
func Load() (*Config, error) {
	trustProxyHeaders, err := getEnvBool("TRUST_PROXY_HEADERS", false)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		HTTPAddr:           getEnv("HTTP_ADDR", ":8080"),
		BaseURL:            getEnv("BASE_URL", "http://localhost:8080"),
		TrustProxyHeaders:  trustProxyHeaders,
		DBDriver:           getEnv("DB_DRIVER", "sqlite"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		AuthMode:           getEnv("AUTH_MODE", "password"),
		AdminEmail:         os.Getenv("ADMIN_EMAIL"),
		AdminPassword:      os.Getenv("ADMIN_PASSWORD"),
		OIDCIssuerURL:      os.Getenv("OIDC_ISSUER_URL"),
		OIDCClientID:       os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret:   os.Getenv("OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:    os.Getenv("OIDC_REDIRECT_URL"),
		AdminAllowedEmails: splitCSV(os.Getenv("ADMIN_ALLOWED_EMAILS")),
		SessionSecret:      os.Getenv("SESSION_SECRET"),
		MealieBaseURL:      strings.TrimSuffix(os.Getenv("MEALIE_BASE_URL"), "/"),
		MealieAPIKey:       os.Getenv("MEALIE_API_KEY"),
		LogLevel:           getEnv("LOG_LEVEL", "info"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// MealieConfigured reports whether a Mealie instance has been configured
// for this deployment — the Meal Library's Mealie import section is hidden
// entirely when it hasn't been.
func (c *Config) MealieConfigured() bool {
	return c.MealieBaseURL != ""
}

// PasswordAuth reports whether Victus is managing its own accounts (the
// default), as opposed to delegating to an external OIDC provider.
func (c *Config) PasswordAuth() bool {
	return c.AuthMode == "password"
}

// SQLite reports whether Victus is configured to use its embedded SQLite
// backend, as opposed to the default Postgres backend.
func (c *Config) SQLite() bool {
	return c.DBDriver == "sqlite"
}

func (c *Config) validate() error {
	var problems []string

	if c.AuthMode != "password" && c.AuthMode != "oidc" {
		problems = append(problems, fmt.Sprintf(`AUTH_MODE must be "password" or "oidc", got %q`, c.AuthMode))
	}
	if c.DBDriver != "postgres" && c.DBDriver != "sqlite" {
		problems = append(problems, fmt.Sprintf(`DB_DRIVER must be "postgres" or "sqlite", got %q`, c.DBDriver))
	}

	required := map[string]string{
		"DATABASE_URL":   c.DatabaseURL,
		"SESSION_SECRET": c.SessionSecret,
	}
	if c.PasswordAuth() {
		required["ADMIN_EMAIL"] = c.AdminEmail
		required["ADMIN_PASSWORD"] = c.AdminPassword
	} else {
		required["OIDC_ISSUER_URL"] = c.OIDCIssuerURL
		required["OIDC_CLIENT_ID"] = c.OIDCClientID
		required["OIDC_CLIENT_SECRET"] = c.OIDCClientSecret
		required["OIDC_REDIRECT_URL"] = c.OIDCRedirectURL
	}
	for name, val := range required {
		if strings.TrimSpace(val) == "" {
			problems = append(problems, "missing required environment variable "+name)
		}
	}

	if len(c.SessionSecret) > 0 && len(c.SessionSecret) < 32 {
		problems = append(problems, "SESSION_SECRET must be at least 32 characters")
	}
	if len(c.AdminPassword) > 0 && len(c.AdminPassword) < 8 {
		problems = append(problems, "ADMIN_PASSWORD must be at least 8 characters")
	}

	urlFields := map[string]string{
		"BASE_URL":          c.BaseURL,
		"OIDC_ISSUER_URL":   c.OIDCIssuerURL,
		"OIDC_REDIRECT_URL": c.OIDCRedirectURL,
		"MEALIE_BASE_URL":   c.MealieBaseURL, // optional, but must be well-formed if set at all
	}
	for name, val := range urlFields {
		if val == "" {
			continue // already reported as missing above, if required
		}
		if !urlutil.IsAbsoluteHTTP(val) {
			problems = append(problems, fmt.Sprintf("%s must be an absolute http(s) URL, got %q", name, val))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// getEnvBool parses key as a bool, returning fallback only when key is
// unset. A value that IS set but isn't a valid bool is a config error, not
// silently ignored — otherwise a typo like TRUST_PROXY_HEADERS=yse would
// quietly disable a security-relevant setting instead of failing loudly.
func getEnvBool(key string, fallback bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid value %q for %s: must be a boolean (true/false)", v, key)
	}
	return b, nil
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

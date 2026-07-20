package config

import (
	"strings"
	"testing"
)

// requiredEnv is the OIDC-mode baseline every existing test in this file
// builds on. Password mode (the default) has its own, separate set of
// TestLoad_PasswordMode_* tests below.
func requiredEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":       "postgres://user:pass@localhost:5432/victus",
		"AUTH_MODE":          "oidc",
		"OIDC_ISSUER_URL":    "https://idp.example.com",
		"OIDC_CLIENT_ID":     "victus",
		"OIDC_CLIENT_SECRET": "secret",
		"OIDC_REDIRECT_URL":  "https://victus.example.com/auth/callback",
		"SESSION_SECRET":     "01234567890123456789012345678901",
	}
}

func withEnv(t *testing.T, overrides map[string]string, fn func()) {
	t.Helper()
	for k, v := range requiredEnv() {
		t.Setenv(k, v)
	}
	for k, v := range overrides {
		t.Setenv(k, v)
	}
	fn()
}

func TestLoad_MissingRequiredVars(t *testing.T) {
	// No env vars set at all — Load should fail listing what's missing.
	if _, err := Load(); err == nil {
		t.Fatal("expected error when required env vars are missing")
	}
}

func TestLoad_Success(t *testing.T) {
	withEnv(t, nil, func() {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.HTTPAddr != ":8080" {
			t.Errorf("expected default HTTP_ADDR, got %q", cfg.HTTPAddr)
		}
	})
}

func TestLoad_SessionSecretTooShort(t *testing.T) {
	withEnv(t, map[string]string{"SESSION_SECRET": "tooshort"}, func() {
		if _, err := Load(); err == nil {
			t.Fatal("expected error for short SESSION_SECRET")
		}
	})
}

func TestLoad_SessionSecretTooShort_StillReportsOtherMissingVars(t *testing.T) {
	t.Setenv("SESSION_SECRET", "tooshort")
	// Deliberately leave every other required var unset.
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "SESSION_SECRET") || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("expected error to mention both the short secret and other missing vars, got: %v", err)
	}
}

func TestLoad_InvalidBoolEnvVar(t *testing.T) {
	withEnv(t, map[string]string{"TRUST_PROXY_HEADERS": "yse"}, func() {
		if _, err := Load(); err == nil {
			t.Fatal("expected error for an unparseable TRUST_PROXY_HEADERS value")
		}
	})
}

func TestLoad_BoolEnvVarUnset_UsesDefault(t *testing.T) {
	withEnv(t, nil, func() {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.TrustProxyHeaders {
			t.Error("expected TrustProxyHeaders to default to false when unset")
		}
	})
}

func TestLoad_MalformedURLFields(t *testing.T) {
	cases := map[string]struct {
		key string
		val string
	}{
		"OIDC_ISSUER_URL not a URL":      {"OIDC_ISSUER_URL", "not a url"},
		"OIDC_ISSUER_URL missing scheme": {"OIDC_ISSUER_URL", "idp.example.com"},
		"OIDC_REDIRECT_URL wrong scheme": {"OIDC_REDIRECT_URL", "ftp://victus.example.com/callback"},
		"BASE_URL missing host":          {"BASE_URL", "https://"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			withEnv(t, map[string]string{tc.key: tc.val}, func() {
				if _, err := Load(); err == nil {
					t.Fatalf("expected error for %s=%q", tc.key, tc.val)
				}
			})
		})
	}
}

func TestLoad_ValidURLFields(t *testing.T) {
	withEnv(t, map[string]string{
		"OIDC_ISSUER_URL":   "https://idp.example.com",
		"OIDC_REDIRECT_URL": "http://localhost:8080/auth/callback", // http is fine for local dev
		"BASE_URL":          "http://localhost:8080",
	}, func() {
		if _, err := Load(); err != nil {
			t.Errorf("unexpected error for well-formed URLs: %v", err)
		}
	})
}

func TestLoad_MealieBaseURL_OptionalByDefault(t *testing.T) {
	withEnv(t, nil, func() {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error with MEALIE_BASE_URL unset: %v", err)
		}
		if cfg.MealieConfigured() {
			t.Error("expected MealieConfigured() to be false when MEALIE_BASE_URL is unset")
		}
	})
}

func TestLoad_MealieBaseURL_MalformedRejected(t *testing.T) {
	withEnv(t, map[string]string{"MEALIE_BASE_URL": "not a url"}, func() {
		if _, err := Load(); err == nil {
			t.Fatal("expected error for a malformed MEALIE_BASE_URL")
		}
	})
}

func TestLoad_MealieBaseURL_TrailingSlashTrimmed(t *testing.T) {
	withEnv(t, map[string]string{"MEALIE_BASE_URL": "https://mealie.example.com/"}, func() {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.MealieBaseURL != "https://mealie.example.com" {
			t.Errorf("MealieBaseURL = %q, want the trailing slash trimmed", cfg.MealieBaseURL)
		}
		if !cfg.MealieConfigured() {
			t.Error("expected MealieConfigured() to be true when MEALIE_BASE_URL is set")
		}
	})
}

// passwordModeEnv is requiredEnv's counterpart for AUTH_MODE=password: no
// OIDC vars needed, but ADMIN_EMAIL/ADMIN_PASSWORD are required instead.
func passwordModeEnv(t *testing.T, overrides map[string]string) {
	t.Helper()
	env := map[string]string{
		"DATABASE_URL":   "postgres://user:pass@localhost:5432/victus",
		"AUTH_MODE":      "password",
		"ADMIN_EMAIL":    "admin@example.com",
		"ADMIN_PASSWORD": "correct-horse-battery-staple",
		"SESSION_SECRET": "01234567890123456789012345678901",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	for k, v := range overrides {
		t.Setenv(k, v)
	}
}

func TestLoad_PasswordMode_Success(t *testing.T) {
	passwordModeEnv(t, nil)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.PasswordAuth() {
		t.Error("expected PasswordAuth() to be true")
	}
}

func TestLoad_AuthMode_DefaultsToPassword(t *testing.T) {
	// AUTH_MODE deliberately left unset — Load must default it to "password"
	// so Victus runs with zero external dependencies out of the box.
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/victus")
	t.Setenv("SESSION_SECRET", "01234567890123456789012345678901")
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("ADMIN_PASSWORD", "correct-horse-battery-staple")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthMode != "password" {
		t.Errorf("AuthMode = %q, want the default %q", cfg.AuthMode, "password")
	}
}

func TestLoad_PasswordMode_MissingAdminCreds(t *testing.T) {
	passwordModeEnv(t, map[string]string{"ADMIN_EMAIL": "", "ADMIN_PASSWORD": ""})
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when ADMIN_EMAIL/ADMIN_PASSWORD are missing in password mode")
	}
	if !strings.Contains(err.Error(), "ADMIN_EMAIL") || !strings.Contains(err.Error(), "ADMIN_PASSWORD") {
		t.Errorf("expected error to mention both missing vars, got: %v", err)
	}
}

func TestLoad_PasswordMode_DoesNotRequireOIDCVars(t *testing.T) {
	passwordModeEnv(t, nil)
	if _, err := Load(); err != nil {
		t.Fatalf("password mode must not require any OIDC_* vars: %v", err)
	}
}

func TestLoad_AdminPasswordTooShort(t *testing.T) {
	passwordModeEnv(t, map[string]string{"ADMIN_PASSWORD": "short"})
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for a short ADMIN_PASSWORD")
	}
	if !strings.Contains(err.Error(), "ADMIN_PASSWORD") {
		t.Errorf("expected error to mention ADMIN_PASSWORD, got: %v", err)
	}
}

func TestLoad_InvalidAuthMode(t *testing.T) {
	passwordModeEnv(t, map[string]string{"AUTH_MODE": "ldap"})
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for an unrecognized AUTH_MODE")
	}
	if !strings.Contains(err.Error(), "AUTH_MODE") {
		t.Errorf("expected error to mention AUTH_MODE, got: %v", err)
	}
}

func TestLoad_OIDCMode_DoesNotRequireAdminCreds(t *testing.T) {
	withEnv(t, nil, func() {
		if _, err := Load(); err != nil {
			t.Fatalf("OIDC mode must not require ADMIN_EMAIL/ADMIN_PASSWORD: %v", err)
		}
	})
}

func TestLoad_DBDriver_DefaultsToSQLite(t *testing.T) {
	withEnv(t, nil, func() {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.DBDriver != "sqlite" {
			t.Errorf("DBDriver = %q, want the default %q", cfg.DBDriver, "sqlite")
		}
		if !cfg.SQLite() {
			t.Error("expected SQLite() to be true for the default driver")
		}
	})
}

func TestLoad_DBDriver_Postgres(t *testing.T) {
	withEnv(t, map[string]string{"DB_DRIVER": "postgres"}, func() {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.SQLite() {
			t.Error("expected SQLite() to be false when DB_DRIVER=postgres")
		}
	})
}

func TestLoad_DBDriver_SQLite(t *testing.T) {
	withEnv(t, map[string]string{"DB_DRIVER": "sqlite", "DATABASE_URL": "/data/victus.db"}, func() {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.SQLite() {
			t.Error("expected SQLite() to be true when DB_DRIVER=sqlite")
		}
	})
}

func TestLoad_InvalidDBDriver(t *testing.T) {
	withEnv(t, map[string]string{"DB_DRIVER": "mysql"}, func() {
		_, err := Load()
		if err == nil {
			t.Fatal("expected error for an unrecognized DB_DRIVER")
		}
		if !strings.Contains(err.Error(), "DB_DRIVER") {
			t.Errorf("expected error to mention DB_DRIVER, got: %v", err)
		}
	})
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV(" a@example.com, b@example.com ,, c@example.com")
	want := []string{"a@example.com", "b@example.com", "c@example.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

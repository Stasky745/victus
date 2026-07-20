package auth_test

import (
	"testing"

	"github.com/Stasky745/victus/internal/auth"
)

func TestEnsureAdminUser_CreatesOnFirstRun(t *testing.T) {
	q := newTestQuerier(t)

	if err := auth.EnsureAdminUser(t.Context(), q, "admin@example.com", "correct-horse-battery"); err != nil {
		t.Fatalf("ensure admin user: %v", err)
	}

	user, err := q.GetUserByEmail(t.Context(), "admin@example.com")
	if err != nil {
		t.Fatalf("get user by email: %v", err)
	}
	if !user.IsAdmin {
		t.Error("bootstrapped user must be an admin")
	}
	if !user.PasswordHash.Valid || user.PasswordHash.String == "" {
		t.Error("bootstrapped user must have a password hash set")
	}
	if !auth.CheckPassword(user.PasswordHash.String, "correct-horse-battery") {
		t.Error("bootstrapped user's password hash doesn't match the password passed in")
	}
}

// TestEnsureAdminUser_Idempotent guards against a real footgun: since
// EnsureAdminUser runs on every startup (not just the first), it must never
// overwrite an existing account — otherwise changing ADMIN_PASSWORD in the
// env, or simply restarting the container, would silently reset a password
// the admin may have since changed from Settings.
func TestEnsureAdminUser_Idempotent(t *testing.T) {
	q := newTestQuerier(t)

	if err := auth.EnsureAdminUser(t.Context(), q, "admin@example.com", "first-password"); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	first, err := q.GetUserByEmail(t.Context(), "admin@example.com")
	if err != nil {
		t.Fatalf("get user by email: %v", err)
	}

	if err := auth.EnsureAdminUser(t.Context(), q, "admin@example.com", "second-password"); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	second, err := q.GetUserByEmail(t.Context(), "admin@example.com")
	if err != nil {
		t.Fatalf("get user by email: %v", err)
	}

	if first.PasswordHash.String != second.PasswordHash.String {
		t.Error("EnsureAdminUser must not overwrite an existing account's password on a later call")
	}
	if !auth.CheckPassword(second.PasswordHash.String, "first-password") {
		t.Error("the original password must still work after a second EnsureAdminUser call with a different password")
	}
}

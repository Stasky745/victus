package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/Stasky745/victus/internal/db/sqlc"
)

// EnsureAdminUser makes sure the ADMIN_EMAIL/ADMIN_PASSWORD account exists,
// creating it as an admin on first run. Idempotent and safe to call on every
// startup: if the account already exists, it's left untouched (this is not
// a password-reset mechanism — change it from Settings once logged in).
func EnsureAdminUser(ctx context.Context, q sqlc.Querier, email, password string) error {
	_, err := q.GetUserByEmail(ctx, email)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows):
		// fall through to create below
	default:
		return fmt.Errorf("look up admin user: %w", err)
	}

	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := q.CreateUserWithPassword(ctx, sqlc.CreateUserWithPasswordParams{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: sql.NullString{String: hash, Valid: true},
		DisplayName:  sql.NullString{String: "Admin", Valid: true},
		IsAdmin:      true,
	}); err != nil {
		return fmt.Errorf("create admin user: %w", err)
	}
	return nil
}

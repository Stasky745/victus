// Package auth implements Victus's OIDC login flow and server-side sessions.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/Stasky745/victus/internal/config"
)

// Authenticator wraps the OIDC provider/verifier and OAuth2 config used for
// the Victus login flow. It works against any standard OIDC provider
// (Pocket ID, Authentik, Keycloak, Authelia, ...).
type Authenticator struct {
	Provider *oidc.Provider
	Verifier *oidc.IDTokenVerifier
	OAuth2   oauth2.Config
}

// NewAuthenticator discovers the OIDC provider at cfg.OIDCIssuerURL and
// builds the OAuth2 config used for the authorization-code + PKCE flow.
func NewAuthenticator(ctx context.Context, cfg *config.Config) (*Authenticator, error) {
	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("discover oidc provider: %w", err)
	}

	return &Authenticator{
		Provider: provider,
		Verifier: provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID}),
		OAuth2: oauth2.Config{
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			RedirectURL:  cfg.OIDCRedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
	}, nil
}

// Claims are the OIDC ID token claims Victus cares about.
type Claims struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

// VerifyIDToken verifies rawIDToken and extracts the claims Victus needs.
func (a *Authenticator) VerifyIDToken(ctx context.Context, rawIDToken string) (*Claims, error) {
	idToken, err := a.Verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("verify id token: %w", err)
	}
	var claims Claims
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}
	return &claims, nil
}

// NewPKCEVerifier returns a fresh PKCE code verifier for one login attempt.
func NewPKCEVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewState returns a fresh random state value for one login attempt.
func NewState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

package httpserver

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/csrf"
	"golang.org/x/oauth2"

	"github.com/Stasky745/victus/internal/auth"
	"github.com/Stasky745/victus/internal/db/sqlc"
	"github.com/Stasky745/victus/internal/httperr"
	"github.com/Stasky745/victus/web/templates/login"
)

const (
	oauthStateCookie    = "victus_oauth_state"
	oauthVerifierCookie = "victus_oauth_verifier"
	oauthFlowTTL        = 5 * time.Minute
)

// handleLogin serves the login entry point for both auth modes: in password
// mode it renders Victus's own login form; in OIDC mode it immediately
// redirects to the configured IdP (there's nothing to render).
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.passwordAuth {
		s.renderLoginForm(w, r, "", http.StatusOK)
		return
	}

	state, err := auth.NewState()
	if err != nil {
		httperr.Internal(w, r, "failed to generate oauth state", err)
		return
	}
	verifier, err := auth.NewPKCEVerifier()
	if err != nil {
		httperr.Internal(w, r, "failed to generate pkce verifier", err)
		return
	}

	auth.SetCookie(w, oauthStateCookie, state, "/auth", oauthFlowTTL, s.secureCookies)
	auth.SetCookie(w, oauthVerifierCookie, verifier, "/auth", oauthFlowTTL, s.secureCookies)

	url := s.oidc.OAuth2.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) renderLoginForm(w http.ResponseWriter, r *http.Request, errMsg string, status int) {
	w.WriteHeader(status)
	if err := login.Page(csrf.Token(r), errMsg).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "failed to render login page", "error", err)
	}
}

// handleLoginSubmit authenticates against Victus's own users table. Only
// registered when password auth is enabled.
func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.renderLoginForm(w, r, "couldn't read the submitted form", http.StatusUnprocessableEntity)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	const invalidCredentials = "invalid email or password"

	user, err := s.queries.GetUserByEmail(ctx, email)
	switch {
	case err == nil:
		// found, checked below
	case errors.Is(err, sql.ErrNoRows):
		s.renderLoginForm(w, r, invalidCredentials, http.StatusUnauthorized)
		return
	default:
		httperr.Internal(w, r, "failed to look up user", err)
		return
	}

	// A user provisioned via OIDC (no local password set) can't log in here —
	// checking a request password against an empty hash would either panic
	// bcrypt or, worse, risk a logic bug that treats "no hash" as "any
	// password matches."
	if !user.PasswordHash.Valid || !auth.CheckPassword(user.PasswordHash.String, password) {
		s.renderLoginForm(w, r, invalidCredentials, http.StatusUnauthorized)
		return
	}

	if err := s.sessions.StartSession(ctx, w, user.ID); err != nil {
		httperr.Internal(w, r, "failed to start session", err, "email", user.Email)
		return
	}

	slog.InfoContext(ctx, "user logged in", "email", user.Email)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	stateCookie, err1 := r.Cookie(oauthStateCookie)
	verifierCookie, err2 := r.Cookie(oauthVerifierCookie)
	if err1 != nil || err2 != nil || r.URL.Query().Get("state") != stateCookie.Value {
		slog.WarnContext(ctx, "oidc callback rejected: missing or mismatched state",
			"has_state_cookie", err1 == nil, "has_verifier_cookie", err2 == nil)
		http.Error(w, "invalid oauth state", http.StatusBadRequest)
		return
	}
	auth.ClearCookie(w, oauthStateCookie, "/auth", s.secureCookies)
	auth.ClearCookie(w, oauthVerifierCookie, "/auth", s.secureCookies)

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	token, err := s.oidc.OAuth2.Exchange(ctx, code, oauth2.VerifierOption(verifierCookie.Value))
	if err != nil {
		slog.Error("oidc token exchange failed", "error", err)
		http.Error(w, "authentication failed", http.StatusBadGateway)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "no id_token in response", http.StatusBadGateway)
		return
	}

	claims, err := s.oidc.VerifyIDToken(ctx, rawIDToken)
	if err != nil {
		slog.ErrorContext(ctx, "oidc id token verification failed", "error", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	if len(s.adminAllowedEmails) > 0 {
		// The allowlist is a security boundary — only trust the email claim
		// for it once the provider has actually confirmed the user owns that
		// address. Without this, an IdP that lets a user set an unverified
		// email (not all are as strict as Pocket ID/Authentik/Keycloak about
		// this) would let anyone self-provision by claiming an allowlisted
		// address.
		if !claims.EmailVerified {
			slog.WarnContext(ctx, "login rejected: email not verified by the OIDC provider", "email", claims.Email)
			http.Error(w, "this account is not allowed to use Victus", http.StatusForbidden)
			return
		}
		if !slices.Contains(s.adminAllowedEmails, claims.Email) {
			slog.WarnContext(ctx, "login rejected: email not in ADMIN_ALLOWED_EMAILS", "email", claims.Email)
			http.Error(w, "this account is not allowed to use Victus", http.StatusForbidden)
			return
		}
	}

	oidcSubject := sql.NullString{String: claims.Subject, Valid: true}
	user, err := s.queries.GetUserByOIDCSubject(ctx, oidcSubject)
	switch {
	case err == nil:
		// existing user, nothing to do
	case errors.Is(err, sql.ErrNoRows):
		user, err = s.queries.CreateUser(ctx, sqlc.CreateUserParams{
			ID:          uuid.New(),
			OidcSubject: oidcSubject,
			Email:       claims.Email,
			DisplayName: sql.NullString{String: claims.Name, Valid: claims.Name != ""},
		})
		if err != nil {
			httperr.Internal(w, r, "failed to provision user", err, "email", claims.Email)
			return
		}
		slog.InfoContext(ctx, "provisioned new user", "email", claims.Email)
	default:
		httperr.Internal(w, r, "failed to look up user", err, "email", claims.Email)
		return
	}

	if err := s.sessions.StartSession(ctx, w, user.ID); err != nil {
		httperr.Internal(w, r, "failed to start session", err, "email", claims.Email)
		return
	}

	slog.InfoContext(ctx, "user logged in", "email", claims.Email)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Not behind RequireAuth (logging out with an already-expired session
	// must still succeed), so resolve the session directly for the log line.
	if user, err := s.sessions.Resolve(r); err == nil {
		slog.InfoContext(r.Context(), "user logged out", "email", user.Email)
	}
	s.sessions.EndSession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

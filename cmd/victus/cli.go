package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Stasky745/victus/internal/auth"
	"github.com/Stasky745/victus/internal/config"
	"github.com/Stasky745/victus/internal/db"
	"github.com/Stasky745/victus/internal/httpserver"
)

// oidcDiscoveryTimeout bounds the startup call to the OIDC issuer's
// /.well-known/openid-configuration — without it, an unreachable or slow
// issuer hangs server startup forever with nothing in the logs to explain why.
const oidcDiscoveryTimeout = 15 * time.Second

func cmdServe(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	setupLogger(cfg.LogLevel)
	slog.Info("starting victus",
		"base_url", cfg.BaseURL,
		"trust_proxy_headers", cfg.TrustProxyHeaders,
		"auth_mode", cfg.AuthMode,
		"admin_allowlist_configured", len(cfg.AdminAllowedEmails) > 0,
	)

	if err := db.Migrate(ctx, cfg.DBDriver, cfg.DatabaseURL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	sqlDB, err := db.Open(ctx, cfg.DBDriver, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	q, err := db.NewQuerier(cfg.DBDriver, sqlDB)
	if err != nil {
		return fmt.Errorf("build querier: %w", err)
	}

	var oidcAuth *auth.Authenticator
	if cfg.PasswordAuth() {
		if err := auth.EnsureAdminUser(ctx, q, cfg.AdminEmail, cfg.AdminPassword); err != nil {
			return fmt.Errorf("bootstrap admin user: %w", err)
		}
	} else {
		discoverCtx, cancel := context.WithTimeout(ctx, oidcDiscoveryTimeout)
		oidcAuth, err = auth.NewAuthenticator(discoverCtx, cfg)
		cancel()
		if err != nil {
			return fmt.Errorf("init oidc authenticator: %w", err)
		}
		slog.Info("oidc provider discovered", "issuer", cfg.OIDCIssuerURL)
	}

	handler, err := httpserver.New(cfg, sqlDB, oidcAuth)
	if err != nil {
		return fmt.Errorf("build http handler: %w", err)
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("victus listening", "addr", cfg.HTTPAddr, "base_url", cfg.BaseURL)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server stopped unexpectedly", "error", err)
			return err
		}
	case <-serveCtx.Done():
		slog.Info("shutdown signal received, draining in-flight requests")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		slog.Info("shutdown complete")
	}
	return nil
}

func cmdMigrate(ctx context.Context, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	setupLogger(cfg.LogLevel)

	sub := "up"
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "up":
		return db.Migrate(ctx, cfg.DBDriver, cfg.DatabaseURL)
	case "down":
		return db.MigrateDown(ctx, cfg.DBDriver, cfg.DatabaseURL)
	case "status":
		return db.MigrateStatus(ctx, cfg.DBDriver, cfg.DatabaseURL)
	default:
		return fmt.Errorf("unknown migrate subcommand %q (expected: up, down, status)", sub)
	}
}

func setupLogger(level string) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}

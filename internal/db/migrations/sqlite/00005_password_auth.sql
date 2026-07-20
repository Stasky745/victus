-- +goose Up
-- SQLite's ALTER TABLE can't change a column's nullability or add a CHECK
-- constraint, so loosening oidc_subject to nullable (and adding the
-- password/admin columns + the auth-method CHECK) needs the standard
-- SQLite "rebuild the table" pattern rather than a Postgres-style ALTER.
CREATE TABLE users_new (
    id            uuid PRIMARY KEY,
    oidc_subject  text UNIQUE,
    email         text NOT NULL UNIQUE,
    display_name  text,
    created_at    timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    password_hash text,
    is_admin      boolean NOT NULL DEFAULT false,
    -- Every user must be able to authenticate somehow: either via an OIDC
    -- identity or a local password (or, in principle, both).
    CHECK (oidc_subject IS NOT NULL OR password_hash IS NOT NULL)
);
INSERT INTO users_new (id, oidc_subject, email, display_name, created_at)
SELECT id, oidc_subject, email, display_name, created_at FROM users;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;

-- +goose Down
CREATE TABLE users_new (
    id           uuid PRIMARY KEY,
    oidc_subject text NOT NULL UNIQUE,
    email        text NOT NULL,
    display_name text,
    created_at   timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO users_new (id, oidc_subject, email, display_name, created_at)
SELECT id, oidc_subject, email, display_name, created_at FROM users WHERE oidc_subject IS NOT NULL;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;

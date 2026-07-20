-- name: GetUserByOIDCSubject :one
SELECT * FROM users WHERE oidc_subject = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at;

-- name: CreateUser :one
INSERT INTO users (id, oidc_subject, email, display_name)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CreateUserWithPassword :one
INSERT INTO users (id, email, password_hash, display_name, is_admin)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateSession :one
INSERT INTO sessions (id, user_id, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetSession :one
SELECT sessions.*, users.email, users.display_name, users.oidc_subject, users.is_admin
FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.id = $1 AND sessions.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = $1;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= now();

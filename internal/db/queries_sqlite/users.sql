-- name: GetUserByOIDCSubject :one
SELECT * FROM users WHERE oidc_subject = ?;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at;

-- name: CreateUser :one
INSERT INTO users (id, oidc_subject, email, display_name)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: CreateUserWithPassword :one
INSERT INTO users (id, email, password_hash, display_name, is_admin)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: CreateSession :one
INSERT INTO sessions (id, user_id, expires_at)
VALUES (?, ?, ?)
RETURNING *;

-- name: GetSession :one
SELECT sessions.*, users.email, users.display_name, users.oidc_subject, users.is_admin
FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.id = ? AND sessions.expires_at > CURRENT_TIMESTAMP;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= CURRENT_TIMESTAMP;

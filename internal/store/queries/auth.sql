-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: CreateUser :one
INSERT INTO users (username, password_hash, role, created_at, password_changed_at)
VALUES (?, ?, 'admin', ?, ?)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ? COLLATE NOCASE;

-- name: SetUserPassword :execrows
UPDATE users SET password_hash = @password_hash, password_changed_at = @changed_at WHERE id = @id;

-- name: SetUserLastLogin :exec
UPDATE users SET last_login_at = ? WHERE id = ?;

-- name: CreateSession :one
INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at, ip, user_agent)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetSessionByToken :one
SELECT sqlc.embed(sessions), sqlc.embed(users)
FROM sessions JOIN users ON users.id = sessions.user_id
WHERE sessions.token_hash = ?;

-- name: ListUserSessions :many
SELECT * FROM sessions WHERE user_id = @user_id AND expires_at > @now ORDER BY last_seen_at DESC, id DESC;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ? WHERE id = ?;

-- name: DeleteSession :execrows
DELETE FROM sessions WHERE id = ?;

-- name: DeleteUserSession :execrows
DELETE FROM sessions WHERE id = @id AND user_id = @user_id;

-- name: DeleteUserSessionsExcept :execrows
DELETE FROM sessions WHERE user_id = @user_id AND id <> @keep_id;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= @now OR last_seen_at <= @idle_before;

-- name: GetAPIKeyByHash :one
SELECT sqlc.embed(api_keys), sqlc.embed(users)
FROM api_keys JOIN users ON users.id = api_keys.user_id
WHERE api_keys.key_hash = ?;

-- name: GetUserAPIKey :one
SELECT * FROM api_keys WHERE user_id = ?;

-- name: DeleteUserAPIKey :execrows
DELETE FROM api_keys WHERE user_id = ?;

-- name: CreateAPIKey :one
INSERT INTO api_keys (user_id, key_hash, last4, created_at) VALUES (?, ?, ?, ?)
RETURNING *;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = ? WHERE id = ?;

-- name: GetFirstUser :one
SELECT * FROM users ORDER BY id LIMIT 1;

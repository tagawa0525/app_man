-- name: CreateSession :exec
INSERT INTO sessions (
  id,
  app_user_id,
  csrf_token,
  created_at,
  last_seen_at,
  expires_at
) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetSessionByID :one
SELECT
  id,
  app_user_id,
  csrf_token,
  created_at,
  last_seen_at,
  expires_at
FROM sessions
WHERE id = ?
LIMIT 1;

-- name: TouchSession :exec
UPDATE sessions
SET last_seen_at = ?
WHERE id = ?;

-- name: RotateSessionID :execrows
UPDATE sessions
SET id = sqlc.arg(new_id)
WHERE id = sqlc.arg(old_id);

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE id = ?;

-- name: DeleteSessionsForAppUser :execrows
DELETE FROM sessions
WHERE app_user_id = ?;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions
WHERE expires_at <= ?;

-- name: BindSessionToAppUser :execrows
UPDATE sessions
SET app_user_id = ?
WHERE id = ?;

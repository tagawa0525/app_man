-- name: GetAppUserByUsername :one
SELECT
  id,
  username,
  password_hash,
  linked_user_id,
  notify_email,
  auth_type,
  disabled_at,
  last_login_at,
  created_at
FROM app_users
WHERE username = ?
LIMIT 1;

-- name: CreateAppUser :one
INSERT INTO app_users (
  username,
  password_hash,
  linked_user_id,
  notify_email,
  auth_type
) VALUES (
  ?, ?, ?, ?, ?
)
RETURNING
  id,
  username,
  password_hash,
  linked_user_id,
  notify_email,
  auth_type,
  disabled_at,
  last_login_at,
  created_at;

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

-- name: GetAppUser :one
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
WHERE id = ?
LIMIT 1;

-- name: ListAppUsersWithRoleCount :many
SELECT
  au.id,
  au.username,
  au.auth_type,
  au.notify_email,
  au.disabled_at,
  u.name AS linked_user_name,
  COUNT(r.id) AS active_role_count
FROM app_users au
LEFT JOIN users u ON u.id = au.linked_user_id
LEFT JOIN user_department_roles r
  ON r.app_user_id = au.id
  AND r.revoked_at IS NULL
GROUP BY au.id
ORDER BY au.username;

-- name: DisableAppUser :execrows
UPDATE app_users
SET disabled_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND disabled_at IS NULL;

-- name: EnableAppUser :execrows
UPDATE app_users
SET disabled_at = NULL
WHERE id = ?
  AND disabled_at IS NOT NULL;

-- name: UpdateAppUserNotifyEmail :execrows
UPDATE app_users
SET notify_email = ?
WHERE id = ?;

-- name: UpdateAppUserPasswordHash :execrows
UPDATE app_users
SET password_hash = ?
WHERE username = ?;

-- name: UpdateAppUserLastLoginAt :exec
UPDATE app_users
SET last_login_at = ?
WHERE id = ?;

-- name: ListUsers :many
SELECT
  id,
  employee_code,
  username,
  name,
  email,
  department_id,
  deactivated_at,
  source,
  source_dn,
  ad_modified_at,
  last_synced_at,
  created_at,
  updated_at
FROM users
WHERE deactivated_at IS NULL
ORDER BY employee_code
LIMIT 200;

-- name: ListUsersIncludingInactive :many
SELECT
  id,
  employee_code,
  username,
  name,
  email,
  department_id,
  deactivated_at,
  source,
  source_dn,
  ad_modified_at,
  last_synced_at,
  created_at,
  updated_at
FROM users
ORDER BY employee_code
LIMIT 200;

-- name: SearchUsers :many
SELECT
  id,
  employee_code,
  username,
  name,
  email,
  department_id,
  deactivated_at,
  source,
  source_dn,
  ad_modified_at,
  last_synced_at,
  created_at,
  updated_at
FROM users
WHERE deactivated_at IS NULL
  AND (
    employee_code LIKE ?1
    OR username LIKE ?1
    OR name LIKE ?1
    OR email LIKE ?1
  )
ORDER BY employee_code
LIMIT 200;

-- name: SearchUsersIncludingInactive :many
SELECT
  id,
  employee_code,
  username,
  name,
  email,
  department_id,
  deactivated_at,
  source,
  source_dn,
  ad_modified_at,
  last_synced_at,
  created_at,
  updated_at
FROM users
WHERE employee_code LIKE ?1
  OR username LIKE ?1
  OR name LIKE ?1
  OR email LIKE ?1
ORDER BY employee_code
LIMIT 200;

-- name: GetUser :one
SELECT
  id,
  employee_code,
  username,
  name,
  email,
  department_id,
  deactivated_at,
  source,
  source_dn,
  ad_modified_at,
  last_synced_at,
  created_at,
  updated_at
FROM users
WHERE id = ?
LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (
  employee_code,
  username,
  name,
  email,
  department_id,
  source
) VALUES (
  ?, ?, ?, ?, ?, 'manual'
)
RETURNING
  id,
  employee_code,
  username,
  name,
  email,
  department_id,
  deactivated_at,
  source,
  source_dn,
  ad_modified_at,
  last_synced_at,
  created_at,
  updated_at;

-- name: UpdateUser :one
UPDATE users
SET
  employee_code = ?,
  username = ?,
  name = ?,
  email = ?,
  department_id = ?,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING
  id,
  employee_code,
  username,
  name,
  email,
  department_id,
  deactivated_at,
  source,
  source_dn,
  ad_modified_at,
  last_synced_at,
  created_at,
  updated_at;

-- name: SoftDeleteUser :execrows
UPDATE users
SET
  deactivated_at = CURRENT_TIMESTAMP,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND deactivated_at IS NULL;

-- name: RestoreUser :execrows
UPDATE users
SET
  deactivated_at = NULL,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND deactivated_at IS NOT NULL;

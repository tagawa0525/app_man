-- name: ListDevices :many
SELECT
  id,
  asset_code,
  hostname,
  primary_user_id,
  department_id,
  retired_at,
  last_seen_at,
  created_at,
  updated_at
FROM devices
WHERE retired_at IS NULL
ORDER BY asset_code
LIMIT 200;

-- name: ListDevicesIncludingInactive :many
SELECT
  id,
  asset_code,
  hostname,
  primary_user_id,
  department_id,
  retired_at,
  last_seen_at,
  created_at,
  updated_at
FROM devices
ORDER BY asset_code
LIMIT 200;

-- name: SearchDevices :many
SELECT
  id,
  asset_code,
  hostname,
  primary_user_id,
  department_id,
  retired_at,
  last_seen_at,
  created_at,
  updated_at
FROM devices
WHERE retired_at IS NULL
  AND (asset_code LIKE ?1 OR hostname LIKE ?1)
ORDER BY asset_code
LIMIT 200;

-- name: SearchDevicesIncludingInactive :many
SELECT
  id,
  asset_code,
  hostname,
  primary_user_id,
  department_id,
  retired_at,
  last_seen_at,
  created_at,
  updated_at
FROM devices
WHERE asset_code LIKE ?1 OR hostname LIKE ?1
ORDER BY asset_code
LIMIT 200;

-- name: GetDevice :one
SELECT
  id,
  asset_code,
  hostname,
  primary_user_id,
  department_id,
  retired_at,
  last_seen_at,
  created_at,
  updated_at
FROM devices
WHERE id = ?
LIMIT 1;

-- name: CreateDevice :one
INSERT INTO devices (
  asset_code, hostname, primary_user_id, department_id
) VALUES (
  ?, ?, ?, ?
)
RETURNING
  id,
  asset_code,
  hostname,
  primary_user_id,
  department_id,
  retired_at,
  last_seen_at,
  created_at,
  updated_at;

-- name: UpdateDevice :one
UPDATE devices
SET
  asset_code = ?,
  hostname = ?,
  primary_user_id = ?,
  department_id = ?,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING
  id,
  asset_code,
  hostname,
  primary_user_id,
  department_id,
  retired_at,
  last_seen_at,
  created_at,
  updated_at;

-- name: SoftDeleteDevice :execrows
UPDATE devices
SET
  retired_at = CURRENT_TIMESTAMP,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND retired_at IS NULL;

-- name: RestoreDevice :execrows
UPDATE devices
SET
  retired_at = NULL,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND retired_at IS NOT NULL;

-- name: GetDeviceByAssetCode :one
SELECT
  id, asset_code, hostname, primary_user_id, department_id,
  retired_at, last_seen_at, created_at, updated_at
FROM devices
WHERE asset_code = ?
LIMIT 1;

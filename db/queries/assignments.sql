-- Comments in this file must stay ASCII-only; sqlc v1.31.1 misparses
-- non-ASCII comments and splits queries incorrectly.

-- ListActiveUserAssignmentsByLicense returns the active (not revoked)
-- user assignments of a license joined with user display columns.
-- deactivated_at is included so the view can flag assignments that
-- still point at deactivated users.
-- name: ListActiveUserAssignmentsByLicense :many
SELECT
  ua.id, ua.user_id,
  u.name AS user_name,
  u.employee_code,
  u.deactivated_at,
  ua.external_account_id, ua.assigned_at, ua.note
FROM user_assignments ua
JOIN users u ON u.id = ua.user_id
WHERE ua.license_id = ? AND ua.revoked_at IS NULL
ORDER BY ua.assigned_at, ua.id;

-- ListActiveDeviceAssignmentsByLicense returns the active (not revoked)
-- device assignments of a license joined with device display columns.
-- retired_at is included so the view can flag assignments that still
-- point at retired devices.
-- name: ListActiveDeviceAssignmentsByLicense :many
SELECT
  da.id, da.device_id,
  d.asset_code,
  d.hostname,
  d.retired_at,
  da.assigned_at, da.note
FROM device_assignments da
JOIN devices d ON d.id = da.device_id
WHERE da.license_id = ? AND da.revoked_at IS NULL
ORDER BY da.assigned_at, da.id;

-- name: CreateUserAssignment :one
INSERT INTO user_assignments (
  license_id,
  user_id,
  external_account_id,
  note
) VALUES (
  ?, ?, ?, ?
)
RETURNING *;

-- name: CreateDeviceAssignment :one
INSERT INTO device_assignments (
  license_id,
  device_id,
  note
) VALUES (
  ?, ?, ?
)
RETURNING *;

-- RevokeUserAssignment marks an assignment as revoked. The handler
-- checks the affected row count: 0 rows means the assignment does not
-- exist, belongs to another license, or is already revoked, and the
-- handler responds with 404 in that case.
-- name: RevokeUserAssignment :execrows
UPDATE user_assignments
SET revoked_at = CURRENT_TIMESTAMP
WHERE id = ? AND license_id = ? AND revoked_at IS NULL;

-- name: RevokeDeviceAssignment :execrows
UPDATE device_assignments
SET revoked_at = CURRENT_TIMESTAMP
WHERE id = ? AND license_id = ? AND revoked_at IS NULL;

-- CountActiveUserAssignment is the application-level duplicate check
-- run before INSERT; the partial unique indexes
-- uniq_user_assignments_active / uniq_device_assignments_active
-- (migration 000006) are the backstop.
-- name: CountActiveUserAssignment :one
SELECT count(*) FROM user_assignments
WHERE license_id = ? AND user_id = ? AND revoked_at IS NULL;

-- name: CountActiveDeviceAssignment :one
SELECT count(*) FROM device_assignments
WHERE license_id = ? AND device_id = ? AND revoked_at IS NULL;

-- CountActiveUserAssignmentsByLicense feeds the over-allocation
-- warning (assigned count versus total_count).
-- name: CountActiveUserAssignmentsByLicense :one
SELECT count(*) FROM user_assignments
WHERE license_id = ? AND revoked_at IS NULL;

-- name: CountActiveDeviceAssignmentsByLicense :one
SELECT count(*) FROM device_assignments
WHERE license_id = ? AND revoked_at IS NULL;

-- GetLicenseUsageByProduct returns the per-product usage summary
-- (owned versus installed versus assigned) from the v_license_usage
-- view defined in migration 000006.
-- name: GetLicenseUsageByProduct :one
SELECT * FROM v_license_usage
WHERE product_id = ?
LIMIT 1;

-- ListActiveUsersForSelect returns every active user for the
-- assignment form options. No LIMIT on purpose: a row missing from
-- the options means that user cannot be assigned at all (the LIMIT
-- 200 list queries are for list pages only).
-- name: ListActiveUsersForSelect :many
SELECT id, employee_code, name
FROM users
WHERE deactivated_at IS NULL
ORDER BY name, id;

-- ListActiveDevicesForSelect returns every active device for the
-- assignment form options. No LIMIT for the same reason as
-- ListActiveUsersForSelect.
-- name: ListActiveDevicesForSelect :many
SELECT id, asset_code, hostname
FROM devices
WHERE retired_at IS NULL
ORDER BY asset_code;

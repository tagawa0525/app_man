-- Comments in this file must stay ASCII-only; sqlc v1.31.1 misparses
-- non-ASCII comments and splits queries incorrectly.

-- name: GetAppSetting :one
SELECT
  key,
  value,
  updated_at,
  updated_by_app_user_id
FROM app_settings
WHERE key = ?
LIMIT 1;

-- ListAppSettings returns every stored setting row. The settings screen
-- (spec 5.11) merges this with the known-key registry; keys absent here
-- fall back to their defaults.
-- name: ListAppSettings :many
SELECT
  key,
  value,
  updated_at,
  updated_by_app_user_id
FROM app_settings
ORDER BY key;

-- UpsertAppSetting inserts or updates one setting (key is the PK).
-- updated_at is bumped on update so the audit trail and the row agree
-- on when the value last changed.
-- name: UpsertAppSetting :one
INSERT INTO app_settings (
  key,
  value,
  updated_by_app_user_id
) VALUES (
  ?, ?, ?
)
ON CONFLICT (key) DO UPDATE SET
  value = excluded.value,
  updated_at = CURRENT_TIMESTAMP,
  updated_by_app_user_id = excluded.updated_by_app_user_id
RETURNING *;

-- DeleteAppSetting removes one setting row. Absence of the key means
-- "use the default" (spec 5.11), so reset-to-default is a DELETE.
-- name: DeleteAppSetting :execrows
DELETE FROM app_settings
WHERE key = ?;

-- name: GetAppSetting :one
SELECT
  key,
  value,
  updated_at,
  updated_by_app_user_id
FROM app_settings
WHERE key = ?
LIMIT 1;

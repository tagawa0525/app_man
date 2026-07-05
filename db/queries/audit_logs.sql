-- Comments in this file must stay ASCII-only; sqlc v1.31.1 misparses
-- non-ASCII comments and splits queries incorrectly.

-- CreateAuditLog appends one audit trail row (spec 8.5: audit log lives
-- in the DB). occurred_at defaults to CURRENT_TIMESTAMP. app_user_id is
-- nullable for actions that happen outside an authenticated session.
-- name: CreateAuditLog :one
INSERT INTO audit_logs (
  app_user_id,
  action,
  entity_type,
  entity_id,
  diff_json
) VALUES (
  ?, ?, ?, ?, ?
)
RETURNING *;

-- ListAuditLogs returns one viewer page (spec 6.1: system_admin audit
-- screen), newest first. app_users is LEFT JOINed so username is NULL
-- for rows written outside an authenticated session (CLI binaries).
-- Filters 1-3 are "empty string means no filter": ?1 action prefix
-- (literal prefix via substr/length -- no LIKE, so % and _ in user
-- input never act as wildcards; sqlc v1.31.1 cannot parse ESCAPE),
-- ?2 entity_type exact, ?3 username exact. ?4 is the id cursor:
-- 0 means first page, otherwise only rows with id < ?4. The CASTs pin
-- the parameter types so sqlc does not infer interface{} (same trick as
-- ListLicenses' include-expired flag). LIMIT is 101 = page size 100 +
-- 1 sentinel row for the handler's has_more check; OFFSET is avoided
-- because it degrades as the log grows and id is AUTOINCREMENT, hence
-- monotonic in time.
-- name: ListAuditLogs :many
SELECT
  a.id, a.app_user_id, a.action, a.entity_type, a.entity_id,
  a.diff_json, a.occurred_at,
  u.username
FROM audit_logs a
LEFT JOIN app_users u ON u.id = a.app_user_id
WHERE (CAST(sqlc.arg(action_prefix) AS TEXT) = '' OR substr(a.action, 1, length(CAST(sqlc.arg(action_prefix) AS TEXT))) = CAST(sqlc.arg(action_prefix) AS TEXT))
  AND (CAST(sqlc.arg(entity_type) AS TEXT) = '' OR a.entity_type = CAST(sqlc.arg(entity_type) AS TEXT))
  AND (CAST(sqlc.arg(username) AS TEXT) = '' OR u.username = CAST(sqlc.arg(username) AS TEXT))
  AND (CAST(sqlc.arg(before_id) AS INTEGER) = 0 OR a.id < CAST(sqlc.arg(before_id) AS INTEGER))
ORDER BY a.id DESC
LIMIT 101;

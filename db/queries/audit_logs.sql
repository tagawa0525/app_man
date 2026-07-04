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

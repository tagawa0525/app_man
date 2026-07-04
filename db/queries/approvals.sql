-- Comments in this file must stay ASCII-only; sqlc v1.31.1 misparses
-- non-ASCII comments and splits queries incorrectly.

-- ListApprovalsForDepartment returns the active (not revoked) approvals
-- of a department. Only the columns the /approvals list actually needs
-- for approval.Evaluate are selected; product display columns come from
-- the separate product listing the caller already loads.
-- name: ListApprovalsForDepartment :many
SELECT a.product_id, a.status, a.scope_type, a.expires_at
FROM department_product_approvals a
WHERE a.department_id = ? AND a.revoked_at IS NULL
ORDER BY a.product_id;

-- GetActiveApproval returns the single active approval row for a
-- (department, product) pair. At most one row can exist thanks to the
-- partial unique index uniq_dept_product_approvals_active. Feeds
-- approval.Evaluate and the "revoke then re-create" edit flow.
-- name: GetActiveApproval :one
SELECT * FROM department_product_approvals
WHERE department_id = ? AND product_id = ? AND revoked_at IS NULL
LIMIT 1;

-- CreateApproval inserts a new active approval. approved_at is stamped
-- server-side; approval_source stays at its 'direct' default (the
-- request flow is out of MVP scope). Editing an approval means revoking
-- the current row first, then inserting a new one (revoked rows are the
-- audit trail).
-- name: CreateApproval :one
INSERT INTO department_product_approvals (
  department_id,
  product_id,
  status,
  scope_type,
  conditions,
  approved_by_app_user_id,
  approved_at,
  expires_at,
  note
) VALUES (
  ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?, ?
)
RETURNING *;

-- RevokeApproval marks an active approval as revoked, recording who and
-- why (revoke_reason is required by the handler for internal control).
-- The handler checks the affected row count: 0 rows means the approval
-- does not exist or is already revoked (404 / conflict).
-- name: RevokeApproval :execrows
UPDATE department_product_approvals
SET
  revoked_at = CURRENT_TIMESTAMP,
  revoked_by_app_user_id = ?,
  revoke_reason = ?,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND revoked_at IS NULL;

-- CountScopeUser reports whether a user is covered by a
-- scope_type='specific_users' approval. Feeds approval.Record.InScope.
-- name: CountScopeUser :one
SELECT count(*) FROM approval_scope_users
WHERE approval_id = ? AND user_id = ?;

-- CountScopeDevice reports whether a device is covered by a
-- scope_type='specific_devices' approval. Feeds approval.Record.InScope.
-- name: CountScopeDevice :one
SELECT count(*) FROM approval_scope_devices
WHERE approval_id = ? AND device_id = ?;

-- UpdateProductDefaultApprovalStatus changes only the company-wide
-- default approval status of a product (/admin/global-approvals).
-- The handler checks the affected row count: 0 rows means the product
-- does not exist (404).
-- name: UpdateProductDefaultApprovalStatus :execrows
UPDATE products
SET default_approval_status = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- ListApprovalHistoryForDeptProduct returns every approval row of a
-- (department, product) pair, revoked rows included, in chronological
-- order. Used by the registration screen to show the audit trail.
-- app_users are LEFT JOINed twice to resolve the approver and revoker
-- display names (both ids are nullable).
-- name: ListApprovalHistoryForDeptProduct :many
SELECT
  a.*,
  au.username AS approved_by_username,
  ru.username AS revoked_by_username
FROM department_product_approvals a
LEFT JOIN app_users au ON au.id = a.approved_by_app_user_id
LEFT JOIN app_users ru ON ru.id = a.revoked_by_app_user_id
WHERE a.department_id = ? AND a.product_id = ?
ORDER BY a.created_at, a.id;

-- Comments in this file must stay ASCII-only; sqlc v1.31.1 misparses
-- non-ASCII comments and splits queries incorrectly.

-- ListRetiredDepartments returns the retired departments
-- (valid_to NOT NULL), the only valid migration sources for the
-- /admin/departments/migrate screen. Ordered by name for the select.
-- name: ListRetiredDepartments :many
SELECT
  id,
  code,
  name,
  parent_id,
  successor_department_id,
  valid_from,
  valid_to,
  source,
  source_ou,
  last_synced_at,
  created_at,
  updated_at
FROM departments
WHERE valid_to IS NOT NULL
ORDER BY name;

-- CountLicensesByDepartment feeds the migration preview: how many
-- licenses the retired department still owns.
-- name: CountLicensesByDepartment :one
SELECT count(*) FROM licenses
WHERE owning_department_id = ?;

-- CountActiveApprovalsByDepartment feeds the migration preview: how
-- many active approvals would be copied to the successor.
-- name: CountActiveApprovalsByDepartment :one
SELECT count(*) FROM department_product_approvals
WHERE department_id = ? AND revoked_at IS NULL;

-- CountConflictingLicenses counts the source-department licenses that
-- MigrateLicensesToDepartment skips because the destination already
-- has a row with the same (product_id, license_slug); moving them
-- would violate UNIQUE(product_id, owning_department_id,
-- license_slug). Used for the preview estimate and, inside the same
-- transaction after the UPDATE, for the skip report (moved rows no
-- longer match, so the count is stable across the UPDATE). Every
-- outer column reference is fully qualified as licenses.* because
-- sqlc reports unqualified ones as ambiguous once the l2 subquery
-- is in scope (same workaround as prior queries).
-- name: CountConflictingLicenses :one
SELECT count(*) FROM licenses
WHERE licenses.owning_department_id = sqlc.arg(from_department_id)
  AND EXISTS (
    SELECT 1 FROM licenses l2
    WHERE l2.product_id = licenses.product_id
      AND l2.owning_department_id = sqlc.arg(to_department_id)
      AND l2.license_slug = licenses.license_slug
  );

-- MigrateLicensesToDepartment moves license ownership from the
-- retired department to its successor in one statement, skipping the
-- rows that would collide with an existing (product_id, license_slug)
-- of the destination (they stay with the retired department and are
-- reported; the operator renames the slug and re-runs). Re-running is
-- safe: already-moved rows no longer match the WHERE clause.
-- name: MigrateLicensesToDepartment :execrows
UPDATE licenses
SET
  owning_department_id = sqlc.arg(to_department_id),
  updated_at = CURRENT_TIMESTAMP
WHERE licenses.owning_department_id = sqlc.arg(from_department_id)
  AND NOT EXISTS (
    SELECT 1 FROM licenses l2
    WHERE l2.product_id = licenses.product_id
      AND l2.owning_department_id = sqlc.arg(to_department_id)
      AND l2.license_slug = licenses.license_slug
  );

-- ListActiveApprovalsForMigration returns every active approval of
-- the retired department with all columns, so the handler can copy
-- each row to the successor via CreateApproval (skipping products the
-- successor already has an active approval for).
-- name: ListActiveApprovalsForMigration :many
SELECT * FROM department_product_approvals
WHERE department_id = ? AND revoked_at IS NULL
ORDER BY product_id;

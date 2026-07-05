-- Comments in this file must stay ASCII-only; sqlc v1.31.1 misparses
-- non-ASCII comments and splits queries incorrectly.

-- The Export* queries feed the /admin/export Excel workbook (spec 5.10,
-- Plan admin-export.md). They return every row -- soft-deleted /
-- revoked / expired ones included -- and have no LIMIT on purpose: the
-- list-page queries cap at 200 rows for display, but the export must be
-- the full data set. app_settings reuses ListAppSettings (already
-- unlimited).

-- name: ExportVendors :many
SELECT * FROM vendors ORDER BY id;

-- name: ExportProducts :many
SELECT * FROM products ORDER BY id;

-- name: ExportDepartments :many
SELECT * FROM departments ORDER BY id;

-- name: ExportUsers :many
SELECT * FROM users ORDER BY id;

-- name: ExportDevices :many
SELECT * FROM devices ORDER BY id;

-- ExportLicenses includes product_keys. The Excel writer emits that
-- column only when the operator opted in (include_keys); callers other
-- than internal/export must not use this query for display purposes.
-- name: ExportLicenses :many
SELECT * FROM licenses ORDER BY id;

-- name: ExportUserAssignments :many
SELECT * FROM user_assignments ORDER BY id;

-- name: ExportDeviceAssignments :many
SELECT * FROM device_assignments ORDER BY id;

-- name: ExportApprovals :many
SELECT * FROM department_product_approvals ORDER BY id;

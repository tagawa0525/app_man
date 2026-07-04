-- Comments in this file must stay ASCII-only; sqlc v1.31.1 misparses
-- non-ASCII comments and splits queries incorrectly.

-- ListLicenses returns display rows joined with product, vendor and
-- owning department names. The first parameter is an include-expired
-- flag: pass 1 to list every license, any other value to list only
-- active ones (expires_at IS NULL or in the future). Rows are ordered
-- by expiry ascending with never-expiring (NULL) rows last.
-- product_keys is intentionally excluded from list rows: they are handed
-- to the view layer as-is and must never carry secret material.
-- name: ListLicenses :many
SELECT
  l.id, l.product_id, l.owning_department_id, l.license_slug,
  l.display_name, l.total_count, l.count_unit, l.contract_type,
  l.purchased_at, l.started_at, l.expires_at, l.vendor_order_no,
  l.purchaser, l.unit_price, l.currency, l.fs_dir_path, l.note,
  l.created_at, l.updated_at,
  p.canonical_name AS product_name,
  ve.name AS vendor_name,
  d.name AS department_name
FROM licenses l
JOIN products p ON p.id = l.product_id
JOIN vendors ve ON ve.id = p.vendor_id
JOIN departments d ON d.id = l.owning_department_id
WHERE (CAST(?1 AS INTEGER) = 1 OR l.expires_at IS NULL OR l.expires_at >= date('now'))
ORDER BY l.expires_at IS NULL, l.expires_at, l.id;

-- name: GetLicenseByID :one
SELECT
  l.*,
  p.canonical_name AS product_name,
  ve.name AS vendor_name,
  d.name AS department_name
FROM licenses l
JOIN products p ON p.id = l.product_id
JOIN vendors ve ON ve.id = p.vendor_id
JOIN departments d ON d.id = l.owning_department_id
WHERE l.id = ?
LIMIT 1;

-- name: CreateLicense :one
INSERT INTO licenses (
  product_id,
  owning_department_id,
  license_slug,
  display_name,
  total_count,
  count_unit,
  contract_type,
  purchased_at,
  started_at,
  expires_at,
  vendor_order_no,
  purchaser,
  unit_price,
  currency,
  product_keys,
  fs_dir_path,
  note
) VALUES (
  ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
)
RETURNING *;

-- name: UpdateLicense :execrows
UPDATE licenses
SET
  product_id = ?,
  owning_department_id = ?,
  license_slug = ?,
  display_name = ?,
  total_count = ?,
  count_unit = ?,
  contract_type = ?,
  purchased_at = ?,
  started_at = ?,
  expires_at = ?,
  vendor_order_no = ?,
  purchaser = ?,
  unit_price = ?,
  currency = ?,
  product_keys = ?,
  fs_dir_path = ?,
  note = ?,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- GetLicenseByKey resolves a license by its natural key
-- (product_id, owning_department_id, license_slug), matching the UNIQUE
-- constraint on licenses. Used by appmgr-import-bootstrap to resolve
-- CSV rows that reference licenses by name instead of by id.
-- name: GetLicenseByKey :one
SELECT * FROM licenses
WHERE product_id = ? AND owning_department_id = ? AND license_slug = ?
LIMIT 1;

-- CountLicensesByFsDirPath counts licenses already using fs_dir_path,
-- excluding the given id (pass 0 when creating a new license). Used by
-- the web layer for suffix-based collision avoidance (spec 3.2).
-- name: CountLicensesByFsDirPath :one
SELECT COUNT(*) FROM licenses
WHERE fs_dir_path = ? AND id != ?;

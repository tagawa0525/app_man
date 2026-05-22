-- name: ListProducts :many
SELECT
  p.id,
  p.vendor_id,
  p.canonical_name,
  p.edition,
  p.software_type,
  p.license_required,
  p.default_approval_status,
  p.canonical_download_url,
  p.service_admin_url,
  p.license_terms_url,
  p.note,
  p.created_at,
  p.updated_at,
  v.name AS vendor_name
FROM products p
JOIN vendors v ON v.id = p.vendor_id
ORDER BY v.name, p.canonical_name, p.edition
LIMIT 200;

-- name: SearchProducts :many
SELECT
  p.id,
  p.vendor_id,
  p.canonical_name,
  p.edition,
  p.software_type,
  p.license_required,
  p.default_approval_status,
  p.canonical_download_url,
  p.service_admin_url,
  p.license_terms_url,
  p.note,
  p.created_at,
  p.updated_at,
  v.name AS vendor_name
FROM products p
JOIN vendors v ON v.id = p.vendor_id
WHERE p.canonical_name LIKE ?1
   OR v.name LIKE ?1
   OR EXISTS (
        SELECT 1
        FROM product_aliases a
        WHERE a.product_id = p.id AND a.alias_name LIKE ?1
      )
ORDER BY v.name, p.canonical_name, p.edition
LIMIT 200;

-- name: GetProduct :one
SELECT
  p.id,
  p.vendor_id,
  p.canonical_name,
  p.edition,
  p.software_type,
  p.license_required,
  p.default_approval_status,
  p.canonical_download_url,
  p.service_admin_url,
  p.license_terms_url,
  p.note,
  p.created_at,
  p.updated_at,
  v.name AS vendor_name
FROM products p
JOIN vendors v ON v.id = p.vendor_id
WHERE p.id = ?
LIMIT 1;

-- name: ListProductsByVendor :many
SELECT
  id,
  vendor_id,
  canonical_name,
  edition,
  software_type,
  license_required,
  default_approval_status,
  canonical_download_url,
  service_admin_url,
  license_terms_url,
  note,
  created_at,
  updated_at
FROM products
WHERE vendor_id = ?
ORDER BY canonical_name, edition
LIMIT 200;

-- name: CreateProduct :one
INSERT INTO products (
  vendor_id,
  canonical_name,
  edition,
  software_type,
  license_required,
  default_approval_status,
  canonical_download_url,
  service_admin_url,
  license_terms_url,
  note
) VALUES (
  ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
)
RETURNING
  id,
  vendor_id,
  canonical_name,
  edition,
  software_type,
  license_required,
  default_approval_status,
  canonical_download_url,
  service_admin_url,
  license_terms_url,
  note,
  created_at,
  updated_at;

-- name: UpdateProduct :one
UPDATE products
SET
  vendor_id = ?,
  canonical_name = ?,
  edition = ?,
  software_type = ?,
  license_required = ?,
  default_approval_status = ?,
  canonical_download_url = ?,
  service_admin_url = ?,
  license_terms_url = ?,
  note = ?,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING
  id,
  vendor_id,
  canonical_name,
  edition,
  software_type,
  license_required,
  default_approval_status,
  canonical_download_url,
  service_admin_url,
  license_terms_url,
  note,
  created_at,
  updated_at;

-- name: DeleteProduct :exec
DELETE FROM products WHERE id = ?;

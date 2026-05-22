-- name: ListVendors :many
SELECT
  id,
  name,
  url,
  note,
  created_at,
  updated_at
FROM vendors
ORDER BY name
LIMIT 200;

-- name: SearchVendors :many
SELECT
  id,
  name,
  url,
  note,
  created_at,
  updated_at
FROM vendors
WHERE name LIKE ?1
ORDER BY name
LIMIT 200;

-- name: GetVendor :one
SELECT
  id,
  name,
  url,
  note,
  created_at,
  updated_at
FROM vendors
WHERE id = ?
LIMIT 1;

-- name: CreateVendor :one
INSERT INTO vendors (
  name,
  url,
  note
) VALUES (
  ?, ?, ?
)
RETURNING
  id,
  name,
  url,
  note,
  created_at,
  updated_at;

-- name: UpdateVendor :one
UPDATE vendors
SET
  name = ?,
  url = ?,
  note = ?,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING
  id,
  name,
  url,
  note,
  created_at,
  updated_at;

-- name: DeleteVendor :exec
DELETE FROM vendors WHERE id = ?;

-- name: CountProductsByVendor :one
SELECT COUNT(*) AS count
FROM products
WHERE vendor_id = ?;

-- name: GetVendorByName :one
SELECT
  id,
  name,
  url,
  note,
  created_at,
  updated_at
FROM vendors
WHERE name = ?
LIMIT 1;

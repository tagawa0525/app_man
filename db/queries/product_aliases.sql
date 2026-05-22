-- name: ListAliasesByProduct :many
SELECT
  id,
  product_id,
  alias_name,
  source,
  created_at
FROM product_aliases
WHERE product_id = ?
ORDER BY alias_name;

-- name: CreateAlias :one
INSERT INTO product_aliases (
  product_id,
  alias_name,
  source
) VALUES (
  ?, ?, 'manual'
)
RETURNING
  id,
  product_id,
  alias_name,
  source,
  created_at;

-- name: DeleteAlias :exec
DELETE FROM product_aliases WHERE id = ?;

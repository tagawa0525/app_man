-- name: ListDepartments :many
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
WHERE valid_to IS NULL
ORDER BY code
LIMIT 200;

-- name: ListDepartmentsIncludingInactive :many
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
ORDER BY code
LIMIT 200;

-- name: SearchDepartments :many
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
WHERE valid_to IS NULL
  AND (name LIKE ?1 OR code LIKE ?1)
ORDER BY code
LIMIT 200;

-- name: SearchDepartmentsIncludingInactive :many
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
WHERE name LIKE ?1 OR code LIKE ?1
ORDER BY code
LIMIT 200;

-- name: GetDepartment :one
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
WHERE id = ?
LIMIT 1;

-- name: ListChildDepartments :many
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
WHERE parent_id = ? AND valid_to IS NULL
ORDER BY code
LIMIT 200;

-- name: ListActiveDepartments :many
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
WHERE valid_to IS NULL
ORDER BY code;

-- name: ListActiveDepartmentsExceptID :many
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
WHERE valid_to IS NULL AND id <> ?
ORDER BY code;

-- name: CreateDepartment :one
INSERT INTO departments (
  code,
  name,
  parent_id,
  successor_department_id,
  valid_from,
  source
) VALUES (
  ?, ?, ?, ?, ?, 'manual'
)
RETURNING
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
  updated_at;

-- name: UpdateDepartment :one
UPDATE departments
SET
  code = ?,
  name = ?,
  parent_id = ?,
  successor_department_id = ?,
  valid_from = ?,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING
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
  updated_at;

-- name: SoftDeleteDepartment :execrows
UPDATE departments
SET
  valid_to = DATE('now'),
  updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND valid_to IS NULL;

-- name: RestoreDepartment :execrows
UPDATE departments
SET
  valid_to = NULL,
  updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND valid_to IS NOT NULL;

-- name: GetDepartmentByCode :one
SELECT
  id, code, name, parent_id, successor_department_id,
  valid_from, valid_to, source, source_ou, last_synced_at,
  created_at, updated_at
FROM departments
WHERE code = ?
LIMIT 1;

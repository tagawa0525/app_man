-- name: CreateUserDepartmentRole :one
INSERT INTO user_department_roles (
  app_user_id,
  department_id,
  role
) VALUES (
  ?, ?, ?
)
RETURNING
  id,
  app_user_id,
  department_id,
  role,
  granted_at,
  revoked_at;

-- name: ListActiveRolesForAppUser :many
SELECT
  role,
  department_id
FROM user_department_roles
WHERE app_user_id = ?
  AND revoked_at IS NULL;

-- name: ListActiveRolesWithDepartmentForAppUser :many
SELECT
  r.id,
  r.role,
  r.department_id,
  d.name AS department_name,
  r.granted_at
FROM user_department_roles r
LEFT JOIN departments d ON d.id = r.department_id
WHERE r.app_user_id = ?
  AND r.revoked_at IS NULL
ORDER BY r.granted_at, r.id;

-- name: GetUserDepartmentRole :one
SELECT
  id,
  app_user_id,
  department_id,
  role,
  granted_at,
  revoked_at
FROM user_department_roles
WHERE id = ?
LIMIT 1;

-- name: RevokeUserDepartmentRole :execrows
UPDATE user_department_roles
SET revoked_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND app_user_id = ?
  AND revoked_at IS NULL;

-- name: CountActiveUserDepartmentRoles :one
SELECT COUNT(*)
FROM user_department_roles
WHERE app_user_id = sqlc.arg(app_user_id)
  AND role = sqlc.arg(role)
  AND revoked_at IS NULL
  AND department_id IS sqlc.narg(department_id);

-- name: CountActiveSystemAdminUsers :one
SELECT COUNT(DISTINCT r.app_user_id)
FROM user_department_roles r
JOIN app_users au ON au.id = r.app_user_id
WHERE r.role = 'system_admin'
  AND r.revoked_at IS NULL
  AND au.disabled_at IS NULL;

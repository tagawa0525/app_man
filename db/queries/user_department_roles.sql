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

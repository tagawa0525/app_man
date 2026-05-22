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

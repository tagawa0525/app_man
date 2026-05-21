DROP VIEW IF EXISTS v_license_usage;

DROP INDEX IF EXISTS idx_audit_logs_occurred;
DROP INDEX IF EXISTS idx_audit_logs_entity;
DROP INDEX IF EXISTS idx_app_users_linked;
DROP INDEX IF EXISTS idx_departments_active;
DROP INDEX IF EXISTS idx_devices_department;
DROP INDEX IF EXISTS idx_users_department;
DROP INDEX IF EXISTS idx_dept_product_approvals_active;
DROP INDEX IF EXISTS idx_device_assignments_active;
DROP INDEX IF EXISTS idx_user_assignments_user;
DROP INDEX IF EXISTS idx_user_assignments_active;
DROP INDEX IF EXISTS idx_installations_device;
DROP INDEX IF EXISTS idx_installations_product;

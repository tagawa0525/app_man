-- Active assignments must be unique per (license, user) and per
-- (license, device). The base table constraints
-- UNIQUE(license_id, user_id, revoked_at) and
-- UNIQUE(license_id, device_id, revoked_at) do not enforce this
-- because SQLite treats NULLs as distinct values, so two rows with
-- revoked_at IS NULL never collide. Partial unique indexes over the
-- active rows (revoked_at IS NULL) close that gap while still
-- allowing re-assignment after revocation.
CREATE UNIQUE INDEX idx_user_assignments_active_unique
  ON user_assignments(license_id, user_id) WHERE revoked_at IS NULL;

CREATE UNIQUE INDEX idx_device_assignments_active_unique
  ON device_assignments(license_id, device_id) WHERE revoked_at IS NULL;

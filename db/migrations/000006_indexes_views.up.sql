CREATE INDEX idx_installations_product ON installations(product_id) WHERE uninstalled_at IS NULL;
CREATE INDEX idx_installations_device ON installations(device_id) WHERE uninstalled_at IS NULL;
CREATE INDEX idx_user_assignments_active ON user_assignments(license_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_user_assignments_user ON user_assignments(user_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_device_assignments_active ON device_assignments(license_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_users_department ON users(department_id) WHERE deactivated_at IS NULL;
CREATE INDEX idx_devices_department ON devices(department_id) WHERE retired_at IS NULL;
CREATE INDEX idx_departments_active ON departments(code) WHERE valid_to IS NULL;
CREATE INDEX idx_app_users_linked ON app_users(linked_user_id) WHERE disabled_at IS NULL;
CREATE INDEX idx_audit_logs_entity ON audit_logs(entity_type, entity_id);
CREATE INDEX idx_audit_logs_occurred ON audit_logs(occurred_at);

-- 「アクティブな割当・承認・ロールが (キー) ごとに高々 1 つ」を保証する partial UNIQUE INDEX。
-- 各テーブルの UNIQUE(..., revoked_at) では revoked_at IS NULL の重複を防げない
-- (SQLite は NULL を distinct 扱いするため) ので、active 行だけを対象に重複禁止する。
-- 履歴側 (revoked_at IS NOT NULL) は UNIQUE 制約に同じタイムスタンプが入る確率が無視できる
-- 前提でテーブル制約に任せる。
CREATE UNIQUE INDEX uniq_user_assignments_active
  ON user_assignments(license_id, user_id)
  WHERE revoked_at IS NULL;
CREATE UNIQUE INDEX uniq_device_assignments_active
  ON device_assignments(license_id, device_id)
  WHERE revoked_at IS NULL;
CREATE UNIQUE INDEX uniq_dept_product_approvals_active
  ON department_product_approvals(department_id, product_id)
  WHERE revoked_at IS NULL;
-- user_department_roles は department_id NULL (= 全社スコープ) も許容するので、
-- NULL / NOT NULL を分けた 2 本立てで「同一 app_user × 同一スコープ × 同一 role」の active 重複を防ぐ。
CREATE UNIQUE INDEX uniq_user_dept_roles_active_dept
  ON user_department_roles(app_user_id, department_id, role)
  WHERE revoked_at IS NULL AND department_id IS NOT NULL;
CREATE UNIQUE INDEX uniq_user_dept_roles_active_global
  ON user_department_roles(app_user_id, role)
  WHERE revoked_at IS NULL AND department_id IS NULL;

CREATE VIEW v_license_usage AS
SELECT
  p.id AS product_id,
  p.canonical_name,
  v.name AS vendor_name,
  COALESCE(SUM(CASE
    WHEN l.expires_at IS NULL OR l.expires_at > date('now') THEN l.total_count
    ELSE 0
  END), 0) AS total_owned,
  (SELECT COUNT(*) FROM installations i
     WHERE i.product_id = p.id AND i.uninstalled_at IS NULL) AS installed_count,
  (SELECT COUNT(*) FROM user_assignments ua
     JOIN licenses l2 ON l2.id = ua.license_id
     WHERE l2.product_id = p.id AND ua.revoked_at IS NULL) AS user_assigned_count,
  (SELECT COUNT(*) FROM device_assignments da
     JOIN licenses l3 ON l3.id = da.license_id
     WHERE l3.product_id = p.id AND da.revoked_at IS NULL) AS device_assigned_count
FROM products p
JOIN vendors v ON v.id = p.vendor_id
LEFT JOIN licenses l ON l.product_id = p.id
GROUP BY p.id;

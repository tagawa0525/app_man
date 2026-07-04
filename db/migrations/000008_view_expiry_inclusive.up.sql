-- Unify the active-license condition to be expiry-date inclusive
-- (expires_at >= date('now')): the expiry day itself is still active,
-- matching the licenses list, the expiring-soon widget and the
-- approvals evaluation semantics. The old strict comparison
-- (> date('now')) dropped a license expiring today from total_owned
-- when expires_at is stored as a date-only string, producing a phantom
-- over-allocation while the same license still appears in the
-- expiring-soon list.
DROP VIEW v_license_usage;
CREATE VIEW v_license_usage AS
SELECT
  p.id AS product_id,
  p.canonical_name,
  v.name AS vendor_name,
  CAST(COALESCE(SUM(CASE
    WHEN l.expires_at IS NULL OR l.expires_at >= date('now') THEN l.total_count
    ELSE 0
  END), 0) AS INTEGER) AS total_owned,
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

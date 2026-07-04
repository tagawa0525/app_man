-- Comments in this file must stay ASCII-only; sqlc v1.31.1 misparses
-- non-ASCII comments and splits queries incorrectly.

-- ListLicenseUsage returns the company-wide per-product usage summary
-- for the dashboard shortage/surplus widget. Columns come from the
-- v_license_usage view; has_unlimited is added because the view folds
-- NULL total_count (= unlimited) licenses into total_owned as 0, which
-- would make an unlimited product look over-allocated. The expiry
-- condition matches the view's active-license condition
-- (expires_at IS NULL OR expires_at >= date('now')).
-- name: ListLicenseUsage :many
SELECT
  u.product_id,
  u.canonical_name,
  u.vendor_name,
  u.total_owned,
  u.installed_count,
  u.user_assigned_count,
  u.device_assigned_count,
  EXISTS(
    SELECT 1 FROM licenses l
    WHERE l.product_id = u.product_id
      AND l.total_count IS NULL
      AND (l.expires_at IS NULL OR l.expires_at >= date('now'))
  ) AS has_unlimited
FROM v_license_usage u
ORDER BY u.vendor_name, u.canonical_name;

-- CountProductsByDefaultApprovalStatus returns the number of products
-- per default_approval_status for the dashboard approval summary
-- widget.
-- name: CountProductsByDefaultApprovalStatus :many
SELECT default_approval_status, count(*) AS product_count
FROM products
GROUP BY default_approval_status
ORDER BY default_approval_status;

-- ListExpiringLicenses returns active licenses expiring within 90 days
-- for the dashboard expiring-licenses widget, soonest first. The lower
-- bound matches ListLicenses' active condition (expires_at >=
-- date('now')). The upper bound uses < date('now', '+91 days') instead
-- of <= date('now', '+90 days') because expires_at values written by
-- the Go driver carry a time-of-day suffix that would sort after the
-- bare date string, silently excluding day 90.
-- name: ListExpiringLicenses :many
SELECT
  l.id, l.display_name, l.expires_at, l.total_count, l.count_unit,
  p.canonical_name AS product_name,
  ve.name AS vendor_name,
  d.name AS department_name
FROM licenses l
JOIN products p ON p.id = l.product_id
JOIN vendors ve ON ve.id = p.vendor_id
JOIN departments d ON d.id = l.owning_department_id
WHERE l.expires_at IS NOT NULL
  AND l.expires_at >= date('now')
  AND l.expires_at < date('now', '+91 days')
ORDER BY l.expires_at, l.id;

-- ListDeactivatedUserAssignments returns active user assignments that
-- still point at deactivated users (spec section 5.14, SQL taken
-- verbatim). Feeds the dashboard leaver widget.
-- name: ListDeactivatedUserAssignments :many
SELECT u.id, u.name, u.department_id, p.canonical_name, ua.assigned_at
FROM user_assignments ua
JOIN users u ON u.id = ua.user_id
JOIN licenses l ON l.id = ua.license_id
JOIN products p ON p.id = l.product_id
WHERE ua.revoked_at IS NULL AND u.deactivated_at IS NOT NULL
ORDER BY u.department_id;

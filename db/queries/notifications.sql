-- Queries for appmgr-notify (spec 5.9): notifications table lifecycle,
-- license expiry detection and recipient resolution.
-- Comments in this file must stay ASCII-only; sqlc v1.31.1 misparses
-- non-ASCII comments and splits queries incorrectly.

-- CreateNotification inserts a pending notification record. Spec 5.9
-- requires a record to exist before any send attempt.
-- name: CreateNotification :one
INSERT INTO notifications (
  kind,
  channel,
  recipient,
  subject,
  body,
  related_entity_type,
  related_entity_id
) VALUES (
  ?, ?, ?, ?, ?, ?, ?
)
RETURNING id, kind, channel, recipient, subject, body,
  related_entity_type, related_entity_id, status, retry_count,
  last_attempted_at, last_error, sent_at, created_at;

-- name: MarkNotificationSent :execrows
UPDATE notifications
SET status = 'sent',
    sent_at = CURRENT_TIMESTAMP,
    last_attempted_at = CURRENT_TIMESTAMP,
    last_error = NULL
WHERE id = ?;

-- MarkNotificationFailed records a failed initial send. retry_count is
-- intentionally not incremented here: it counts --retry-failed attempts
-- only, so a record becomes gave_up after notification_max_retry
-- retries regardless of the initial failure.
-- name: MarkNotificationFailed :execrows
UPDATE notifications
SET status = 'failed',
    last_attempted_at = CURRENT_TIMESTAMP,
    last_error = ?
WHERE id = ?;

-- IncrementNotificationRetry records the outcome of one failed retry
-- attempt: bumps retry_count and sets status as decided by the caller
-- ('failed' to keep retrying, 'gave_up' when retry_count reaches
-- notification_max_retry). Successful retries use MarkNotificationSent.
-- name: IncrementNotificationRetry :execrows
UPDATE notifications
SET retry_count = retry_count + 1,
    status = sqlc.arg(status),
    last_attempted_at = CURRENT_TIMESTAMP,
    last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(id);

-- name: ListFailedNotificationsForRetry :many
SELECT id, kind, channel, recipient, subject, body,
  related_entity_type, related_entity_id, status, retry_count,
  last_attempted_at, last_error, sent_at, created_at
FROM notifications
WHERE status = 'failed'
  AND retry_count < ?
ORDER BY id;

-- CountSentNotificationForEvent implements the duplicate-suppression
-- key (kind, related_entity_type, related_entity_id) from spec 5.9:
-- when a sent record already exists for the event, no new record is
-- created. Callers pass non-NULL related_* values.
-- name: CountSentNotificationForEvent :one
SELECT count(*) FROM notifications
WHERE kind = ?
  AND related_entity_type = ?
  AND related_entity_id = ?
  AND status = 'sent';

-- CountNotificationsByKindSince supports the daily gave_up summary
-- dedup: pass kind='gave_up_summary' and the UTC start of today to
-- check whether a summary was already created today.
-- CountSentNotificationForEvent cannot be reused because summary rows
-- have NULL related_entity_type/id and '=' never matches NULL.
-- name: CountNotificationsByKindSince :one
SELECT count(*) FROM notifications
WHERE kind = ?
  AND created_at >= ?;

-- ListUnsummarizedGaveUp returns gave_up notifications not yet covered
-- by a gave_up_summary: rows whose last attempt happened after the most
-- recent summary record was created (all gave_up rows when no summary
-- exists yet). gave_up rows always carry last_attempted_at because the
-- retry flow sets it on every attempt.
-- name: ListUnsummarizedGaveUp :many
SELECT id, kind, channel, recipient, subject, body,
  related_entity_type, related_entity_id, status, retry_count,
  last_attempted_at, last_error, sent_at, created_at
FROM notifications
WHERE status = 'gave_up'
  AND kind != 'gave_up_summary'
  AND last_attempted_at > COALESCE(
    (SELECT MAX(s.created_at) FROM notifications s
     WHERE s.kind = 'gave_up_summary'),
    '1970-01-01 00:00:00')
ORDER BY id;

-- ListLicensesExpiringInDays returns licenses whose expires_at (UTC
-- date; date('now') is UTC in SQLite) is exactly N days from today,
-- joined with product, vendor and owning department names for the
-- notification body. julianday of two plain dates differs by an exact
-- integer, so the CAST comparison is safe.
-- name: ListLicensesExpiringInDays :many
SELECT
  l.id,
  l.display_name,
  l.expires_at,
  l.owning_department_id,
  p.canonical_name AS product_name,
  ve.name AS vendor_name,
  d.name AS department_name
FROM licenses l
JOIN products p ON p.id = l.product_id
JOIN vendors ve ON ve.id = p.vendor_id
JOIN departments d ON d.id = l.owning_department_id
WHERE l.expires_at IS NOT NULL
  AND CAST(julianday(date(l.expires_at)) - julianday(date('now')) AS INTEGER) = CAST(sqlc.arg(days) AS INTEGER)
ORDER BY l.id;

-- ListLicenseManagerEmailsForDepartment returns active license_manager
-- app users for a department with their email candidates. Recipient
-- resolution order (spec 5.9): notify_email first, then the linked
-- user's email; callers warn and skip when both are empty. DISTINCT
-- guards against duplicate active role rows (the UNIQUE constraint
-- treats NULL revoked_at values as distinct in SQLite).
-- name: ListLicenseManagerEmailsForDepartment :many
SELECT DISTINCT
  au.id AS app_user_id,
  au.username,
  au.notify_email,
  u.email AS linked_user_email
FROM user_department_roles r
JOIN app_users au ON au.id = r.app_user_id
LEFT JOIN users u ON u.id = au.linked_user_id
WHERE r.role = 'license_manager'
  AND r.department_id = ?
  AND r.revoked_at IS NULL
  AND au.disabled_at IS NULL
ORDER BY au.id;

-- ListSystemAdminEmails returns active system_admin app users with
-- their email candidates. Used as fallback recipients when a
-- department has no license_manager, and for the gave_up daily summary.
-- name: ListSystemAdminEmails :many
SELECT DISTINCT
  au.id AS app_user_id,
  au.username,
  au.notify_email,
  u.email AS linked_user_email
FROM user_department_roles r
JOIN app_users au ON au.id = r.app_user_id
LEFT JOIN users u ON u.id = au.linked_user_id
WHERE r.role = 'system_admin'
  AND r.revoked_at IS NULL
  AND au.disabled_at IS NULL
ORDER BY au.id;

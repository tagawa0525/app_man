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

-- ListFailedNotificationsForRetry returns failed notifications still
-- eligible for retry. Rows are excluded (superseded) when a sent record
-- already exists for the same (kind, channel, recipient, related_*):
-- the daily run re-creates events whose only records are failed (spec
-- dedup checks sent only), so once that re-created record is delivered,
-- retrying the stale failed row would double-send. IS is used for the
-- related_* comparison because summary rows carry NULLs and '=' never
-- matches NULL.
-- name: ListFailedNotificationsForRetry :many
SELECT id, kind, channel, recipient, subject, body,
  related_entity_type, related_entity_id, status, retry_count,
  last_attempted_at, last_error, sent_at, created_at
FROM notifications n
WHERE n.status = 'failed'
  AND n.retry_count < ?
  AND NOT EXISTS (
    SELECT 1 FROM notifications s
    WHERE s.status = 'sent'
      AND s.kind = n.kind
      AND s.channel = n.channel
      AND s.recipient = n.recipient
      AND s.related_entity_type IS n.related_entity_type
      AND s.related_entity_id IS n.related_entity_id
  )
ORDER BY n.id;

-- CountSupersededFailedNotifications counts the failed rows that
-- ListFailedNotificationsForRetry excludes by the sent-exists rule, so
-- the retry run can report them as skipped_superseded.
-- name: CountSupersededFailedNotifications :one
SELECT count(*) FROM notifications n
WHERE n.status = 'failed'
  AND n.retry_count < ?
  AND EXISTS (
    SELECT 1 FROM notifications s
    WHERE s.status = 'sent'
      AND s.kind = n.kind
      AND s.channel = n.channel
      AND s.recipient = n.recipient
      AND s.related_entity_type IS n.related_entity_type
      AND s.related_entity_id IS n.related_entity_id
  );

-- CountSentNotificationForEvent implements the duplicate-suppression
-- key (kind, channel, related_entity_type, related_entity_id) from spec
-- 5.9: when a sent record already exists for the event on the channel,
-- no new record is created. The channel is part of the key so that a
-- multi partial success re-sends only on channels that have not
-- succeeded yet. Callers pass non-NULL related_* values.
-- name: CountSentNotificationForEvent :one
SELECT count(*) FROM notifications
WHERE kind = ?
  AND channel = ?
  AND related_entity_type = ?
  AND related_entity_id = ?
  AND status = 'sent';

-- CountNotificationsByKindOnDay supports the daily gave_up summary
-- dedup: pass kind='gave_up_summary' and the UTC day as a YYYY-MM-DD
-- string. The date-prefix comparison via substr works for both storage
-- formats (CURRENT_TIMESTAMP "YYYY-MM-DD HH:MM:SS" and the Go driver's
-- "... +0000 UTC"); binding a raw time.Time instead would miss rows
-- created exactly at midnight because of the string format difference.
-- Equality (not >=) is used so that a future-dated row (clock skew)
-- cannot permanently suppress every later daily summary.
-- CountSentNotificationForEvent cannot be reused because summary rows
-- have NULL related_entity_type/id.
-- name: CountNotificationsByKindOnDay :one
SELECT count(*) FROM notifications
WHERE kind = ?
  AND substr(CAST(created_at AS TEXT), 1, 10) = CAST(sqlc.arg(day) AS TEXT);

-- ListUnsummarizedGaveUp returns gave_up notifications not yet covered
-- by a delivered gave_up_summary: rows whose last attempt happened
-- after the most recent *sent* summary was created (all gave_up rows
-- when no sent summary exists yet). gave_up rows always carry
-- last_attempted_at because the retry flow sets it on every attempt.
-- The checkpoint deliberately ignores non-sent summaries: a failed
-- summary never reached the admins, so letting it advance the
-- checkpoint would hide those gave_up rows from every future summary.
-- A failed summary is also re-sent by --retry-failed with its original
-- body; if that retry later succeeds after the next daily run already
-- re-summarized, the same rows can be reported twice. Duplicated
-- listing is accepted as safer than permanent omission.
-- name: ListUnsummarizedGaveUp :many
SELECT id, kind, channel, recipient, subject, body,
  related_entity_type, related_entity_id, status, retry_count,
  last_attempted_at, last_error, sent_at, created_at
FROM notifications
WHERE status = 'gave_up'
  AND kind != 'gave_up_summary'
  AND last_attempted_at > COALESCE(
    (SELECT MAX(s.created_at) FROM notifications s
     WHERE s.kind = 'gave_up_summary'
       AND s.status = 'sent'),
    '1970-01-01 00:00:00')
ORDER BY id;

-- ListLicensesExpiringOn returns licenses whose expires_at falls on the
-- given UTC date ('YYYY-MM-DD', computed by the caller as today+N),
-- joined with product, vendor and owning department names for the
-- notification body. The comparison uses substr(...,1,10) instead of
-- date(expires_at): the Go sqlite driver stores time.Time values in Go's
-- time.String() format ("2026-08-08 00:00:00 +0000 UTC"), which SQLite's
-- date() cannot parse (it returns NULL, so no row would ever match).
-- substr works for both that format and CURRENT_TIMESTAMP-style text.
-- name: ListLicensesExpiringOn :many
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
  AND substr(CAST(l.expires_at AS TEXT), 1, 10) = CAST(sqlc.arg(expires_on) AS TEXT)
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

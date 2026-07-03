-- name: CountPrunableAuditLogs :one
SELECT count(*) FROM audit_logs
WHERE occurred_at < ?;

-- name: PruneAuditLogs :execrows
DELETE FROM audit_logs
WHERE occurred_at < ?;

-- name: CountPrunableRawInstallations :one
SELECT count(*) FROM raw_installations
WHERE created_at < ?;

-- name: PruneRawInstallations :execrows
DELETE FROM raw_installations
WHERE created_at < ?;

-- name: CountPrunableImportLogs :one
SELECT count(*) FROM import_logs
WHERE imported_at < ?
  AND NOT EXISTS (SELECT 1 FROM raw_installations r WHERE r.import_log_id = import_logs.id);

-- name: PruneImportLogs :execrows
DELETE FROM import_logs
WHERE imported_at < ?
  AND NOT EXISTS (SELECT 1 FROM raw_installations r WHERE r.import_log_id = import_logs.id);

-- name: CountPrunableSentNotifications :one
SELECT count(*) FROM notifications
WHERE sent_at IS NOT NULL AND sent_at < ?;

-- name: PruneSentNotifications :execrows
DELETE FROM notifications
WHERE sent_at IS NOT NULL AND sent_at < ?;

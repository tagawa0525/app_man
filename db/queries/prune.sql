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

-- CountPrunableImportLogs intentionally differs from PruneImportLogs.
-- The real run deletes raw_installations before import_logs, so a parent
-- whose children are all prunable raw rows is deleted in the same run.
-- To predict the post-run state, exclude only parents keeping a child
-- that survives this run. Bind the raw cutoff to the second parameter.
-- (Comment kept ASCII-only; sqlc v1.31.1 misparses non-ASCII comments.)
-- name: CountPrunableImportLogs :one
SELECT count(*) FROM import_logs
WHERE imported_at < ?
  AND NOT EXISTS (
    SELECT 1 FROM raw_installations r
    WHERE r.import_log_id = import_logs.id
      AND r.created_at >= ?
  );

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

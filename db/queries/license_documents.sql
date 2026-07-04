-- Comments in this file must stay ASCII-only; sqlc v1.31.1 misparses
-- non-ASCII comments and splits queries incorrectly.

-- ListLicenseDocumentsByLicense returns the documents of one license in
-- upload order (uploaded_at ascending, id as a deterministic tiebreaker).
-- name: ListLicenseDocumentsByLicense :many
SELECT * FROM license_documents
WHERE license_id = ?
ORDER BY uploaded_at, id;

-- CreateLicenseDocument inserts one uploaded document row. stored_path is
-- the path relative to file_store.base_path with / separators; sha256 and
-- mime_type come from the filestore validation (magic-byte sniffing).
-- name: CreateLicenseDocument :one
INSERT INTO license_documents (
  license_id,
  doc_type,
  stored_path,
  original_filename,
  sha256,
  mime_type,
  size_bytes,
  uploaded_by_app_user_id,
  note
) VALUES (
  ?, ?, ?, ?, ?, ?, ?, ?, ?
)
RETURNING *;

-- name: GetLicenseDocumentByID :one
SELECT * FROM license_documents
WHERE id = ?
LIMIT 1;

-- UpdateLicenseDocumentStoredPath rewrites the stored_path of one document.
-- Used when a license fs_dir_path changes (directory rename on slug change):
-- the web layer re-prefixes each matching path so the DB keeps pointing at
-- the moved files.
-- name: UpdateLicenseDocumentStoredPath :execrows
UPDATE license_documents
SET stored_path = ?
WHERE id = ?;

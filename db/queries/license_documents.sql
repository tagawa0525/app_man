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

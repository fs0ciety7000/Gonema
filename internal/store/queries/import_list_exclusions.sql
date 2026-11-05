-- name: AddImportListExclusion :exec
INSERT INTO import_list_exclusions (tmdb_id, title, year, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (tmdb_id) DO NOTHING;

-- name: IsImportListExcluded :one
SELECT EXISTS (SELECT 1 FROM import_list_exclusions WHERE tmdb_id = ?);

-- name: ListImportListExclusions :many
SELECT * FROM import_list_exclusions ORDER BY title, tmdb_id;

-- name: DeleteImportListExclusion :execrows
DELETE FROM import_list_exclusions WHERE tmdb_id = ?;

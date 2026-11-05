-- Health checks (internal/health) and queue repair (manual import retry).

-- ListFileSizesInRootFolder returns the size of every movie file of a root
-- folder, smallest first (the health check takes the median).
-- name: ListFileSizesInRootFolder :many
SELECT f.size
FROM movie_files f
JOIN movies m ON m.id = f.movie_id
WHERE m.root_folder_id = sqlc.arg(root_folder_id)
ORDER BY f.size;

-- ListFailedImports returns the downloads waiting in warning after a failed
-- import, with the time of the last failure (import_failed history row, else
-- the completion time).
-- name: ListFailedImports :many
SELECT d.id, d.movie_id, d.client_id, d.release_title, d.output_path, d.status_messages,
       CAST(coalesce((SELECT max(h.created_at) FROM history h
                       WHERE h.download_id = d.id AND h.event = 'import_failed'),
                     d.completed_at, d.grabbed_at) AS INTEGER) AS failed_at
FROM downloads d
WHERE d.status = 'warning' AND d.completed_at IS NOT NULL
ORDER BY d.id;

-- ReassignDownloadMovie points a completed (or failed import) download at
-- another movie, before a manual import for that movie.
-- name: ReassignDownloadMovie :one
UPDATE downloads SET movie_id = sqlc.arg(movie_id)
WHERE id = sqlc.arg(id) AND status IN ('completed', 'warning')
RETURNING *;

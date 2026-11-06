-- name: AppendHistory :one
INSERT INTO history (movie_id, event, source_title, quality, custom_score, download_id, data, created_at)
VALUES (sqlc.narg(movie_id), sqlc.arg(event), sqlc.arg(source_title), sqlc.narg(quality),
        sqlc.narg(custom_score), sqlc.narg(download_id), sqlc.arg(data), sqlc.arg(created_at))
RETURNING *;

-- ListMovieHistory is keyset-paginated, newest first: pass the last id of the
-- previous page, or math.MaxInt64 first.
-- name: ListMovieHistory :many
SELECT * FROM history
WHERE movie_id = CAST(sqlc.arg(movie_id) AS INTEGER) AND id < sqlc.arg(before_id)
ORDER BY id DESC
LIMIT sqlc.arg(limit);

-- name: ListRecentHistory :many
SELECT * FROM history
WHERE id < sqlc.arg(before_id)
ORDER BY id DESC
LIMIT sqlc.arg(limit);

-- PurgeHistory deletes rows created before cutoff. The history_retention_only
-- trigger aborts unless every such row is also older than the
-- 'history.retention_days' setting.
-- name: PurgeHistory :execrows
DELETE FROM history WHERE created_at < sqlc.arg(cutoff);

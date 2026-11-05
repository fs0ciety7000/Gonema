-- UpsertCollection refreshes TMDB fields; user settings are kept on conflict.
-- name: UpsertCollection :one
INSERT INTO collections (tmdb_id, name, overview, poster_path, backdrop_path, refreshed_at)
VALUES (sqlc.arg(tmdb_id), sqlc.arg(name), sqlc.arg(overview), sqlc.narg(poster_path),
        sqlc.narg(backdrop_path), sqlc.arg(refreshed_at))
ON CONFLICT (tmdb_id) DO UPDATE SET
    name          = excluded.name,
    overview      = excluded.overview,
    poster_path   = excluded.poster_path,
    backdrop_path = excluded.backdrop_path,
    refreshed_at  = excluded.refreshed_at
RETURNING *;

-- name: GetCollection :one
SELECT * FROM collections WHERE id = ?;

-- name: GetCollectionByTmdbID :one
SELECT * FROM collections WHERE tmdb_id = ?;

-- name: ListCollections :many
SELECT * FROM collections ORDER BY name, id;

-- name: UpdateCollectionSettings :one
UPDATE collections SET
    monitored            = sqlc.arg(monitored),
    quality_profile_id   = sqlc.narg(quality_profile_id),
    root_folder_id       = sqlc.narg(root_folder_id),
    minimum_availability = sqlc.arg(minimum_availability)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListCollectionMovies :many
SELECT * FROM movies WHERE collection_id = CAST(sqlc.arg(collection_id) AS INTEGER) ORDER BY year, sort_title, id;

-- name: DeleteCollection :execrows
DELETE FROM collections WHERE id = ?;

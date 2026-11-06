-- Queries of the importer (internal/importer).

-- UpsertMovieFile records the main file of a movie, replacing the previous one
-- (one file per movie, v1). The row id is kept on replacement.
-- name: UpsertMovieFile :one
INSERT INTO movie_files (
    movie_id, relative_path, size, quality, revision, edition, languages,
    release_group, scene_name, parsed, media_info, custom_score, imported_at
) VALUES (
    sqlc.arg(movie_id), sqlc.arg(relative_path), sqlc.arg(size), sqlc.arg(quality),
    sqlc.arg(revision), sqlc.arg(edition), sqlc.arg(languages), sqlc.arg(release_group),
    sqlc.arg(scene_name), sqlc.arg(parsed), sqlc.arg(media_info), sqlc.arg(custom_score),
    sqlc.arg(imported_at)
)
ON CONFLICT (movie_id) DO UPDATE SET
    relative_path = excluded.relative_path,
    size          = excluded.size,
    quality       = excluded.quality,
    revision      = excluded.revision,
    edition       = excluded.edition,
    languages     = excluded.languages,
    release_group = excluded.release_group,
    scene_name    = excluded.scene_name,
    parsed        = excluded.parsed,
    media_info    = excluded.media_info,
    custom_score  = excluded.custom_score,
    imported_at   = excluded.imported_at
RETURNING *;

-- ListRootFolderMovies lists the movies of a root folder with their file, for
-- the existing library import.
-- name: ListRootFolderMovies :many
SELECT m.id, m.tmdb_id, m.title, m.year, m.path, f.id AS file_id
FROM movies m
LEFT JOIN movie_files f ON f.movie_id = m.id
WHERE m.root_folder_id = sqlc.arg(root_folder_id)
ORDER BY m.path;

-- SetMoviePath points a movie at an existing folder of its root folder (existing
-- library import: the folder keeps its name).
-- name: SetMoviePath :one
UPDATE movies SET path = sqlc.arg(path) WHERE id = sqlc.arg(id) RETURNING *;

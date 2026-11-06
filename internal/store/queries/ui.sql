-- Queries of the web UI screens (library grid and table, header, saved views).

-- ListLibraryIndex returns the compact row of every movie, in id order. The
-- library service keeps it in memory (rebuilt when GetLibraryVersion changes)
-- and filters and sorts it in Go. rating_tmdb is -1 without a TMDB rating.
-- download_progress is the persisted progress
-- of the newest active download (0 when not downloading).
-- name: ListLibraryIndex :many
SELECT m.id, m.tmdb_id, m.imdb_id, m.title, m.original_title, m.sort_title, m.year, m.status, m.runtime_min,
       m.genres, m.ratings, m.poster_path, m.dominant_color, m.monitored, m.quality_profile_id,
       m.minimum_availability, m.root_folder_id, m.path, m.added_at, m.collection_id,
       m.release_cinema, m.release_digital, m.release_physical,
       CAST(coalesce(json_extract(m.ratings, '$.tmdb.value'), -1) AS REAL) AS rating_tmdb,
       s.state,
       f.quality AS file_quality, f.size AS file_size, f.custom_score AS file_score,
       f.edition AS file_edition,
       CAST(coalesce((SELECT group_concat(h.value, ' ') FROM json_each(f.media_info, '$.hdr') h), '') AS TEXT) AS file_hdr,
       CAST(CASE WHEN s.state = 'downloading' THEN coalesce(
           (SELECT d.progress FROM downloads d
             WHERE d.movie_id = m.id
               AND d.status IN ('queued', 'downloading', 'paused', 'completed', 'importing', 'warning')
             ORDER BY d.grabbed_at DESC, d.id DESC LIMIT 1), 0)
       ELSE 0 END AS REAL) AS download_progress,
       f.imported_at AS file_imported_at
FROM movies m
JOIN movie_state s ON s.movie_id = m.id
LEFT JOIN movie_files f ON f.movie_id = m.id
ORDER BY m.id;

-- GetLibraryVersion is bumped by triggers on every change ListLibraryIndex
-- can see (migration 00004).
-- name: GetLibraryVersion :one
SELECT v FROM library_version WHERE id = 1;

-- ListLastFailedDownloads returns the movies whose newest download failed,
-- with its grab time (the library index compares it with the file import).
-- name: ListLastFailedDownloads :many
SELECT d.movie_id, d.grabbed_at FROM downloads d
WHERE d.status = 'failed'
  AND NOT EXISTS (SELECT 1 FROM downloads n
                   WHERE n.movie_id = d.movie_id
                     AND (n.grabbed_at > d.grabbed_at OR (n.grabbed_at = d.grabbed_at AND n.id > d.id)));

-- name: ListAllMovieTags :many
SELECT movie_id, tag_id FROM movie_tags ORDER BY movie_id, tag_id;

-- SearchAlternateTitleMovies returns the movies with an alternate title that
-- contains query (lower case; lower() folds ASCII only).
-- name: SearchAlternateTitleMovies :many
SELECT DISTINCT movie_id FROM alternate_titles
WHERE instr(lower(title), CAST(sqlc.arg(query) AS TEXT)) > 0;

-- name: ListCollectionMovieStates :many
SELECT m.id, m.tmdb_id, m.title, m.year, m.poster_path, m.dominant_color, m.monitored, s.state
FROM movies m
JOIN movie_state s ON s.movie_id = m.id
WHERE m.collection_id = CAST(sqlc.arg(collection_id) AS INTEGER)
ORDER BY m.year, m.sort_title, m.id;

-- ----------------------------------------------------------------- saved views
-- Views are shared (user_id NULL) until users exist (phase 8).

-- name: ListSavedViews :many
SELECT * FROM saved_views
WHERE user_id IS NULL
  AND (CAST(sqlc.narg(page) AS TEXT) IS NULL OR page = CAST(sqlc.narg(page) AS TEXT))
ORDER BY page, position, id;

-- name: GetSavedView :one
SELECT * FROM saved_views WHERE id = ? AND user_id IS NULL;

-- name: CreateSavedView :one
INSERT INTO saved_views (user_id, page, name, filters, sort, columns, density, position)
VALUES (NULL, sqlc.arg(page), sqlc.arg(name), sqlc.arg(filters), sqlc.arg(sort), sqlc.arg(columns),
        sqlc.arg(density), sqlc.arg(position))
RETURNING *;

-- name: UpdateSavedView :one
UPDATE saved_views SET
    page     = sqlc.arg(page),
    name     = sqlc.arg(name),
    filters  = sqlc.arg(filters),
    sort     = sqlc.arg(sort),
    columns  = sqlc.arg(columns),
    density  = sqlc.arg(density),
    position = sqlc.arg(position)
WHERE id = sqlc.arg(id) AND user_id IS NULL
RETURNING *;

-- name: DeleteSavedView :execrows
DELETE FROM saved_views WHERE id = ? AND user_id IS NULL;

-- FindImageOwner finds the movie an image belongs to (demo image rendering).
-- name: FindImageOwner :one
SELECT id, title, year, dominant_color FROM movies
WHERE poster_path = CAST(sqlc.arg(image_path) AS TEXT) OR backdrop_path = CAST(sqlc.arg(image_path) AS TEXT)
ORDER BY id LIMIT 1;

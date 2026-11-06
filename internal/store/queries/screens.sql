-- Queries of the web UI screens of phase 7 lot 2: tags, history, queue,
-- discover flags and file operations.

-- ----------------------------------------------------------------- tags

-- name: ListTagsWithUsage :many
SELECT t.id, t.label,
       CAST((SELECT count(*) FROM movie_tags mt WHERE mt.tag_id = t.id) AS INTEGER) AS movies
FROM tags t
ORDER BY t.label, t.id;

-- name: GetTag :one
SELECT * FROM tags WHERE id = ?;

-- name: RenameTag :one
UPDATE tags SET label = sqlc.arg(label) WHERE id = sqlc.arg(id) RETURNING *;

-- name: CountTagMovies :one
SELECT count(*) FROM movie_tags WHERE tag_id = ?;

-- ----------------------------------------------------------------- history

-- ListHistoryFiltered is the history screen: keyset-paginated by id, newest
-- first, with the movie (NULL once removed from the library) and the retry link
-- of the download. events is a JSON array of event kinds; NULL filters nothing.
-- since and until are Unix milliseconds (until excluded).
-- name: ListHistoryFiltered :many
SELECT h.id, h.movie_id, h.event, h.source_title, h.quality, h.custom_score, h.download_id, h.data, h.created_at,
       m.title AS movie_title, m.year AS movie_year, m.poster_path AS movie_poster_path,
       m.dominant_color AS movie_dominant_color, d.retry_of AS download_retry_of
FROM history h
LEFT JOIN movies m ON m.id = h.movie_id
LEFT JOIN downloads d ON d.id = h.download_id
WHERE h.id < sqlc.arg(before_id)
  AND (CAST(sqlc.narg(events) AS TEXT) IS NULL
       OR h.event IN (SELECT value FROM json_each(CAST(sqlc.narg(events) AS TEXT))))
  AND (CAST(sqlc.narg(movie_id) AS INTEGER) IS NULL OR h.movie_id = CAST(sqlc.narg(movie_id) AS INTEGER))
  AND (CAST(sqlc.narg(download_id) AS INTEGER) IS NULL OR h.download_id = CAST(sqlc.narg(download_id) AS INTEGER))
  AND (CAST(sqlc.narg(since) AS INTEGER) IS NULL OR h.created_at >= CAST(sqlc.narg(since) AS INTEGER))
  AND (CAST(sqlc.narg(until) AS INTEGER) IS NULL OR h.created_at < CAST(sqlc.narg(until) AS INTEGER))
ORDER BY h.id DESC
LIMIT sqlc.arg(limit);

-- ListDownloadLinks returns the retry links of downloads (ids: JSON array).
-- name: ListDownloadLinks :many
SELECT id, retry_of FROM downloads
WHERE id IN (SELECT value FROM json_each(CAST(sqlc.arg(ids) AS TEXT)));

-- ----------------------------------------------------------------- queue

-- ListQueueContext returns what the queue screen shows around each active
-- download: its movie, indexer and seeders (from the stored decision), the
-- quality of the file it would replace, its attempt number (grabs of the movie
-- in the 24 hours up to it, as counted by automatic retries) and the download
-- it retries.
-- name: ListQueueContext :many
SELECT d.id, d.movie_id,
       m.title AS movie_title, m.year AS movie_year, m.poster_path AS movie_poster_path,
       m.dominant_color AS movie_dominant_color,
       CAST(coalesce(i.name, json_extract(d.decision, '$.indexer'), '') AS TEXT) AS indexer_name,
       CAST(coalesce(json_extract(d.decision, '$.seeders'), 0) AS INTEGER) AS seeders,
       f.quality AS file_quality,
       CAST((SELECT count(*) FROM downloads g
              WHERE g.movie_id = d.movie_id AND g.grabbed_at >= d.grabbed_at - 86400000
                AND (g.grabbed_at < d.grabbed_at OR (g.grabbed_at = d.grabbed_at AND g.id <= d.id))) AS INTEGER) AS attempt,
       p.id AS previous_id, p.release_title AS previous_title, p.status AS previous_status,
       p.status_messages AS previous_messages
FROM downloads d
JOIN movies m ON m.id = d.movie_id
LEFT JOIN indexers i ON i.id = d.indexer_id
LEFT JOIN movie_files f ON f.movie_id = d.movie_id
LEFT JOIN downloads p ON p.id = d.retry_of
WHERE d.status IN ('queued', 'downloading', 'paused', 'completed', 'importing', 'warning')
ORDER BY d.grabbed_at, d.id;

-- ----------------------------------------------------------------- discover

-- ListMoviesByTmdbIDs flags provider results already in the library
-- (tmdb_ids: JSON array).
-- name: ListMoviesByTmdbIDs :many
SELECT m.id, m.tmdb_id, m.monitored, s.state
FROM movies m
JOIN movie_state s ON s.movie_id = m.id
WHERE m.tmdb_id IN (SELECT value FROM json_each(CAST(sqlc.arg(tmdb_ids) AS TEXT)));

-- ListExcludedTmdbIDs returns which of tmdb_ids (JSON array) are on the import
-- list exclusions.
-- name: ListExcludedTmdbIDs :many
SELECT tmdb_id FROM import_list_exclusions
WHERE tmdb_id IN (SELECT value FROM json_each(CAST(sqlc.arg(tmdb_ids) AS TEXT)));

-- ----------------------------------------------------------------- file operations

-- name: SetMovieLocation :one
UPDATE movies SET root_folder_id = sqlc.arg(root_folder_id), path = sqlc.arg(path)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetMovieAtLocation :one
SELECT id, title, year FROM movies WHERE root_folder_id = sqlc.arg(root_folder_id) AND path = sqlc.arg(path);

-- name: SetMovieFileRelativePath :one
UPDATE movie_files SET relative_path = sqlc.arg(relative_path)
WHERE movie_id = sqlc.arg(movie_id)
RETURNING *;

-- name: DeleteMovieFile :execrows
DELETE FROM movie_files WHERE movie_id = ?;
